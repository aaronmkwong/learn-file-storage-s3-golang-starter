package main

import (
	"fmt"
	"net/http"
	"io"
	"encoding/base64"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	
	// Parse the video ID from the URL path
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Extract the JWT from the Authorization header
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	// Validate the JWT and retrieve the authenticated user ID
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// Parse the multipart form
	// Allow up to 10 MB of in-memory form data before spilling to disk
	const maxMemory = 10 << 20 // 10 MB

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse multipart form", err)
		return
	}

	// Retrieve the uploaded file
	// The form field must be named "thumbnail"
	file, fileHeader, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't retrieve uploaded file", err)
		return
	}
	defer file.Close()

	// Extract the media type from the uploaded file's headers
	// Example: image/png or image/jpeg
	mediaType := fileHeader.Header.Get("Content-Type")

	// Read the uploaded image into memory
	imageData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read uploaded file", err)
		return
	}

	// Fetch the video and verify ownership
	// Only the video's owner may upload a thumbnail
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't retrieve video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not authorized to modify this video", nil)
		return
	}

	// Encode the image bytes as base64 and build a data URL
	encoded := base64.StdEncoding.EncodeToString(imageData)
	url := "data:" + mediaType + ";base64," + encoded

	video.ThumbnailURL = &url

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	// Return the updated video
	respondWithJSON(w, http.StatusOK, video)
}
