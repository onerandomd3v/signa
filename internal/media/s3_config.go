package media

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

func NewConfiguredS3Storage(ctx context.Context, settings S3Config) (Storage, error) {
	if settings.Endpoint == "" || settings.Region == "" || settings.Bucket == "" || settings.AccessKeyID == "" || settings.SecretAccessKey == "" {
		return nil, ErrStorageUnavailable
	}
	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(settings.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(settings.AccessKeyID, settings.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load object storage config: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(settings.Endpoint)
		options.UsePathStyle = true
	})
	return NewS3Storage(settings.Bucket, client, s3.NewPresignClient(client)), nil
}
