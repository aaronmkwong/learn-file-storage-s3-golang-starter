package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {

	// Limit uploads to 1 GB.
	const maxUploadSize = 1 << 30 // 1 GB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// Parse the video ID from the request URL.
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
		return
	}

	// Authenticate the user.
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Retrieve the video metadata and verify ownership.
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't retrieve video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not authorized to modify this video", nil)
		return
	}

	// Parse the multipart form.
	err = r.ParseMultipartForm(maxUploadSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse multipart form", err)
		return
	}

	// Retrieve the uploaded video file.
	// The multipart form field must be named "video".
	file, fileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't retrieve uploaded video", err)
		return
	}
	defer file.Close()

	// Validate the uploaded media type.
	// Only MP4 videos are accepted.
	mediaType, _, err := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Video must be an MP4", nil)
		return
	}

	// Create a temporary file for the uploaded video.
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create temporary file", err)
		return
	}

	// Close executes before Remove because defers are LIFO.
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy the uploaded video into the temporary file.
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save uploaded video", err)
		return
	}

	// Reset the file pointer so S3 reads from the beginning.
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't rewind temporary file", err)
		return
	}

	// Generate a random 32-byte object key encoded as hexadecimal.
	// Final format: <64-character-hex-string>.mp4
	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate object key", err)
		return
	}

	objectKey := hex.EncodeToString(randomBytes) + ".mp4"

	// Upload the file to Amazon S3.
	_, err = cfg.s3Client.PutObject(
		context.TODO(),
		&s3.PutObjectInput{
			Bucket:      &cfg.s3Bucket,
			Key:         &objectKey,
			Body:        tempFile,
			ContentType: &mediaType,
		},
	)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video to S3", err)
		return
	}

	// Build the S3 URL and save it to the video record.
	videoURL := fmt.Sprintf(
		"https://%s.s3.%s.amazonaws.com/%s",
		cfg.s3Bucket,
		cfg.s3Region,
		objectKey,
	)

	video.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video metadata", err)
		return
	}

	// Return the updated video metadata.
	respondWithJSON(w, http.StatusOK, video)
}