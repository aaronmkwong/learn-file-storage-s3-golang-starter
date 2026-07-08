
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"mime"
	"path/filepath"
	"strings"

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
	const maxMemory = 10 << 20 // 10 MB

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse multipart form", err)
		return
	}

	// Retrieve the uploaded file
	file, fileHeader, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't retrieve uploaded file", err)
		return
	}
	defer file.Close()

	// Parse the Content-Type header to determine the media type.
	mediaType, _, err := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}

	// Only JPEG and PNG thumbnails are allowed.
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Thumbnail must be a JPEG or PNG image", nil)
		return
	}

	// Fetch the video and verify ownership.
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't retrieve video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not authorized to modify this video", nil)
		return
	}

	// Determine the file extension from the Content-Type header.
	// Examples:
	// image/png  -> png
	// image/jpeg -> jpeg
	parts := strings.Split(mediaType, "/")
	if len(parts) != 2 {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", nil)
		return
	}
	extension := parts[1]

	// Build the full path where the thumbnail will be stored.
	fileName := fmt.Sprintf("%s.%s", videoID.String(), extension)
	filePath := filepath.Join(cfg.assetsRoot, fileName)

	// Create the destination file.
	dst, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create thumbnail file", err)
		return
	}
	defer dst.Close()

	// Copy the uploaded file directly to disk.
	_, err = io.Copy(dst, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save thumbnail", err)
		return
	}

	// Build the URL that will be served by the /assets file server.
	url := fmt.Sprintf(
		"http://localhost:%s/assets/%s",
		cfg.port,
		fileName,
	)

	video.ThumbnailURL = &url

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	// Return the updated video.
	respondWithJSON(w, http.StatusOK, video)
}