package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Client interface {
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type PresignClient interface {
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type S3Storage struct {
	bucket  string
	client  S3Client
	presign PresignClient
}

func NewS3Storage(bucket string, client S3Client, presign PresignClient) *S3Storage {
	return &S3Storage{bucket: bucket, client: client, presign: presign}
}

func (s *S3Storage) PresignPut(ctx context.Context, objectKey, contentType string, expires time.Duration) (string, map[string]string, time.Time, error) {
	if s == nil || s.client == nil || s.presign == nil || s.bucket == "" {
		return "", nil, time.Time{}, ErrStorageUnavailable
	}
	request, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		ContentType: aws.String(contentType),
	}, func(options *s3.PresignOptions) { options.Expires = expires })
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign media upload: %w", err)
	}
	return request.URL, map[string]string{"Content-Type": contentType}, time.Now().UTC().Add(expires), nil
}

func (s *S3Storage) Head(ctx context.Context, objectKey string) (ObjectMetadata, error) {
	if s == nil || s.client == nil || s.bucket == "" {
		return ObjectMetadata{}, ErrStorageUnavailable
	}
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	if err != nil {
		var responseError interface{ ErrorCode() string }
		if errors.As(err, &responseError) && responseError.ErrorCode() == http.StatusText(http.StatusNotFound) {
			return ObjectMetadata{}, ErrObjectNotFound
		}
		return ObjectMetadata{}, fmt.Errorf("head media object: %w", err)
	}
	contentType := ""
	if output.ContentType != nil {
		contentType = *output.ContentType
	}
	sizeBytes := int64(0)
	if output.ContentLength != nil {
		sizeBytes = *output.ContentLength
	}
	return ObjectMetadata{ContentType: contentType, SizeBytes: sizeBytes}, nil
}
