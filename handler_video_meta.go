package main

import (
	"encoding/json"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

// handlerVideoMetaCreate creates a new video metadata record for the
// authenticated user. The actual video file is uploaded separately.
func (cfg *apiConfig) handlerVideoMetaCreate(w http.ResponseWriter, r *http.Request) {

	// Define the request parameters and embed the database create parameters.
	type parameters struct {
		database.CreateVideoParams
	}

	// Extract the JWT from the Authorization header.
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	// Validate the JWT and retrieve the authenticated user ID.
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Decode the request body into the video creation parameters.
	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err = decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't decode parameters", err)
		return
	}

	// Associate the new video with the authenticated user.
	params.UserID = userID

	// Create the video metadata record in the database.
	video, err := cfg.db.CreateVideo(params.CreateVideoParams)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create video", err)
		return
	}

	// Return the newly created video metadata.
	respondWithJSON(w, http.StatusCreated, video)
}

// handlerVideoMetaDelete deletes a video metadata record after verifying
// that the authenticated user owns the video.
func (cfg *apiConfig) handlerVideoMetaDelete(w http.ResponseWriter, r *http.Request) {

	// Parse the video ID from the URL path.
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Extract the JWT from the Authorization header.
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	// Validate the JWT and retrieve the authenticated user ID.
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Retrieve the video metadata from the database.
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't get video", err)
		return
	}

	// Ensure that only the video owner can delete it.
	if video.UserID != userID {
		respondWithError(w, http.StatusForbidden, "You can't delete this video", err)
		return
	}

	// Delete the video metadata record from the database.
	err = cfg.db.DeleteVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't delete video", err)
		return
	}

	// Return a successful response with no content.
	w.WriteHeader(http.StatusNoContent)
}

// handlerVideoGet retrieves a single video by ID and returns it with
// a temporary presigned URL for the private S3 object.
func (cfg *apiConfig) handlerVideoGet(w http.ResponseWriter, r *http.Request) {

	// Parse the video ID from the URL path.
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
		return
	}

	// Retrieve the video from the database.
	// The database stores VideoURL as <bucket>,<key>.
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't get video", err)
		return
	}

	// Convert the stored S3 bucket/key into a temporary presigned URL.
	signedVideo, err := cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"Couldn't generate signed video URL",
			err,
		)
		return
	}

	// Return the video with its temporary presigned URL.
	respondWithJSON(w, http.StatusOK, signedVideo)
}

// handlerVideosRetrieve retrieves all videos belonging to the
// authenticated user and returns each with a temporary presigned URL.
func (cfg *apiConfig) handlerVideosRetrieve(w http.ResponseWriter, r *http.Request) {

	// Extract the JWT from the Authorization header.
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	// Validate the JWT and retrieve the authenticated user ID.
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Retrieve the user's videos from the database.
	videos, err := cfg.db.GetVideos(userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't retrieve videos", err)
		return
	}

	// Convert each database video, which stores the S3 bucket and key,
	// into a video containing a temporary presigned URL.
	signedVideos := make([]database.Video, 0, len(videos))

	for _, video := range videos {
		signedVideo, err := cfg.dbVideoToSignedVideo(video)
		if err != nil {
			respondWithError(
				w,
				http.StatusInternalServerError,
				"Couldn't generate signed video URL",
				err,
			)
			return
		}

		signedVideos = append(signedVideos, signedVideo)
	}

	// Return the videos with temporary presigned URLs.
	respondWithJSON(w, http.StatusOK, signedVideos)
}