package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	TypeImage = "image"
	TypeAudio = "audio"
	TypeVideo = "video"

	UploadURLLifetime = 10 * time.Minute
)

var ErrStorageUnavailable = errors.New("media storage unavailable")
var ErrObjectNotFound = errors.New("media object not found")

type ObjectMetadata struct {
	ContentType string
	SizeBytes   int64
}

type Storage interface {
	PresignPut(ctx context.Context, objectKey, contentType string, expires time.Duration) (string, map[string]string, time.Time, error)
	Head(ctx context.Context, objectKey string) (ObjectMetadata, error)
}

func Validate(mediaType, contentType string, sizeBytes int64) error {
	if sizeBytes <= 0 {
		return fmt.Errorf("size_bytes must be greater than zero")
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	maxSize := maxSize(mediaType)
	if maxSize == 0 {
		return fmt.Errorf("unsupported media_type %q", mediaType)
	}
	if !allowedContentTypes[mediaType][contentType] {
		return fmt.Errorf("unsupported content_type %q for media_type %q", contentType, mediaType)
	}
	if sizeBytes > maxSize {
		return fmt.Errorf("size_bytes exceeds %s media limit", mediaType)
	}
	return nil
}

func ObjectKey(reportID string) string {
	return fmt.Sprintf("reports/%s/media/%s", reportID, uuid.NewString())
}

func IsObjectKeyForReport(objectKey, reportID string) bool {
	prefix := fmt.Sprintf("reports/%s/media/", reportID)
	return strings.HasPrefix(objectKey, prefix) && len(objectKey) > len(prefix) && !strings.Contains(objectKey[len(prefix):], "/")
}

func maxSize(mediaType string) int64 {
	switch mediaType {
	case TypeImage:
		return 10 << 20
	case TypeAudio:
		return 25 << 20
	case TypeVideo:
		return 100 << 20
	default:
		return 0
	}
}

var allowedContentTypes = map[string]map[string]bool{
	TypeImage: {"image/jpeg": true, "image/png": true, "image/webp": true},
	TypeAudio: {"audio/mpeg": true, "audio/mp4": true, "audio/ogg": true, "audio/webm": true},
	TypeVideo: {"video/mp4": true, "video/webm": true},
}
