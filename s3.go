package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

// generatePresignedURL creates a temporary presigned URL that can be used
// to download an object from a private S3 bucket.
func generatePresignedURL(
	s3Client *s3.Client,
	bucket string,
	key string,
	expireTime time.Duration,
) (string, error) {

	// Create a presign client using the existing S3 client.
	presignClient := s3.NewPresignClient(s3Client)

	// Generate a presigned GET request for the requested S3 object.
	presignedRequest, err := presignClient.PresignGetObject(
		context.TODO(),
		&s3.GetObjectInput{
			Bucket: &bucket,
			Key:    &key,
		},
		s3.WithPresignExpires(expireTime),
	)
	if err != nil {
		return "", err
	}

	// Return the temporary signed URL.
	return presignedRequest.URL, nil
}

// dbVideoToSignedVideo converts the database representation of a video,
// which stores the S3 bucket and object key as: <bucket>,<key>
// into a video containing a temporary presigned URL.
func (cfg *apiConfig) dbVideoToSignedVideo(
	video database.Video,
) (database.Video, error) {

	// If the video has not been uploaded yet, there is no S3 object
	// to sign. Return the video unchanged.
	if video.VideoURL == nil || *video.VideoURL == "" {
		return video, nil
	}

	// Split the stored bucket,key value into its two components.
	parts := strings.SplitN(*video.VideoURL, ",", 2)

	// Ensure both the bucket and key are present.
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return video, fmt.Errorf("invalid stored video URL format")
	}

	bucket := parts[0]
	key := parts[1]

	// Generate a temporary presigned URL for the S3 object.
	presignedURL, err := generatePresignedURL(
		cfg.s3Client,
		bucket,
		key,
		15*time.Minute,
	)
	if err != nil {
		return video, err
	}

	// Replace the database bucket,key value with the temporary
	// presigned URL in the copy of the video being returned.
	video.VideoURL = &presignedURL

	return video, nil
}