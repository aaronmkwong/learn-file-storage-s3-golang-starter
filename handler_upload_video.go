package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

// processVideoForFastStart creates a new MP4 file optimized for "fast start" playback.
// Fast start moves the MP4 metadata (the moov atom) toward the beginning
// of the file, allowing playback to begin before the entire video has
// finished downloading.
func processVideoForFastStart(filePath string) (string, error) {

	// Create the output path by appending ".processing" to the
	// original temporary file path.
	outputFilePath := filePath + ".processing"

	// Run ffmpeg with stream copying enabled.
	// -i          input file
	// -c copy     copy the existing audio/video streams without re-encoding
	// -movflags   faststart; move MP4 metadata to the beginning of the file
	// -f mp4      force the output format to MP4
	cmd := exec.Command(
		"ffmpeg",
		"-i", filePath,
		"-c", "copy",
		"-movflags", "faststart",
		"-f", "mp4",
		outputFilePath,
	)

	// Capture ffmpeg's diagnostic output.
	// ffmpeg writes most of its logs and errors to stderr.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Execute ffmpeg.
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf(
			"ffmpeg failed: %w: %s",
			err,
			stderr.String(),
		)
	}

	// Return the path to the processed file.
	return outputFilePath, nil
}

// getVideoAspectRatio uses ffprobe to inspect a video file and returns
// one of three aspect-ratio classifications: "16:9", "9:16", "other"
// The function searches specifically for a video stream rather than
// assuming that the first stream returned by ffprobe is the video stream.
func getVideoAspectRatio(filePath string) (string, error) {

	// Run ffprobe and request stream metadata in JSON format.
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		filePath,
	)

	// Capture ffprobe's standard output in a buffer.
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Execute the ffprobe command.
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	// Define the subset of ffprobe's JSON output that we need.
	// ffprobe may return multiple streams, such as video, audio, and
	// subtitles, so CodecType identifies the video stream.
	var probeData struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}

	// Parse ffprobe's JSON output.
	err = json.Unmarshal(stdout.Bytes(), &probeData)
	if err != nil {
		return "", err
	}

	// Search through all streams and process only the video stream.
	for _, stream := range probeData.Streams {
		if stream.CodecType != "video" {
			continue
		}

		// Avoid division by zero if ffprobe returns an invalid height.
		if stream.Height == 0 {
			return "other", nil
		}

		// Calculate the video's width-to-height ratio.
		ratio := float64(stream.Width) / float64(stream.Height)

		// Allow a small tolerance because video dimensions may be slightly
		// different from the exact mathematical ratio.
		const tolerance = 0.01

		// 16:9 landscape video.
		if math.Abs(ratio-(16.0/9.0)) < tolerance {
			return "16:9", nil
		}

		// 9:16 portrait video.
		if math.Abs(ratio-(9.0/16.0)) < tolerance {
			return "9:16", nil
		}

		// A valid video stream was found, but it is neither 16:9 nor 9:16.
		return "other", nil
	}

	// No video stream was found.
	return "other", nil
}

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

	// Defer cleanup of the temporary file.
	// Defers execute in LIFO order, so Close runs before Remove.
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy the uploaded video into the temporary file.
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save uploaded video", err)
		return
	}

	// Process the video for fast-start playback.
	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"Couldn't process video for fast start",
			err,
		)
		return
	}

	// Ensure the processed file is removed after the request completes.
	defer os.Remove(processedFilePath)

	// Determine the video's aspect ratio.
	// ffprobe opens the file independently using its path, so no Seek is
	// required before this call.
	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't determine video aspect ratio", err)
		return
	}

	// Assign an S3 key prefix based on the video's aspect ratio.
	var aspectPrefix string
	switch aspectRatio {
	case "16:9":
		aspectPrefix = "landscape"
	case "9:16":
		aspectPrefix = "portrait"
	default:
		aspectPrefix = "other"
	}

	// Open the processed file for uploading to S3.
	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"Couldn't open processed video",
			err,
		)
		return
	}
	defer processedFile.Close()

	// Generate a random 32-byte object key encoded as hexadecimal.
	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate object key", err)
		return
	}

	// Use "/" to create an S3 key prefix.
	// Example: landscape/<64-character-hex-string>.mp4
	objectKey := fmt.Sprintf(
		"%s/%s.mp4",
		aspectPrefix,
		hex.EncodeToString(randomBytes),
	)

	// Upload the processed video to Amazon S3.
	_, err = cfg.s3Client.PutObject(
		context.TODO(),
		&s3.PutObjectInput{
			Bucket:      &cfg.s3Bucket,
			Key:         &objectKey,
			Body:        processedFile,
			ContentType: &mediaType,
		},
	)
	if err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"Couldn't upload video to S3",
			err,
		)
		return
	}

	// Build the CloudFront URL for the uploaded video.
	// The URL is stored directly in the database since CloudFront
	// serves the video rather than a temporary presigned S3 URL.
	videoURL := fmt.Sprintf(
		"%s/%s",
		cfg.s3CfDistribution,
		objectKey,
	)

	video.VideoURL = &videoURL

	// Update the video metadata in the database.
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"Couldn't update video metadata",
			err,
		)
		return
	}

	// Return the updated video metadata.
	respondWithJSON(w, http.StatusOK, video)
	
}
