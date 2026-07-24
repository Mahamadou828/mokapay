package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ErrObjectNotFound is returned by Head when the key does not exist.
var ErrObjectNotFound = errors.New("object not found")

// S3Config points at real S3 when Endpoint is empty (credentials resolve
// through the default chain), or at any S3-compatible store (MinIO locally)
// when Endpoint + static keys are set. MinIO needs UsePathStyle.
type S3Config struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

// S3 - object storage client over one bucket. Uploads and reads go through
// presigned URLs so file bytes never transit our services.
type S3 struct {
	bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(cfg.Region)}
	if cfg.AccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &S3{
		bucket:  cfg.Bucket,
		client:  client,
		presign: s3.NewPresignClient(client),
	}, nil
}

// PresignPut returns a URL the client PUTs the bytes to. The content type is
// part of the signature — a PUT with a different Content-Type header fails.
func (s *S3) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presigning put %s: %w", key, err)
	}

	return req.URL, nil
}

// PresignGet returns a time-limited read URL; the bucket itself stays private.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presigning get %s: %w", key, err)
	}

	return req.URL, nil
}

// Head returns the stored object's size and etag; ErrObjectNotFound when the
// key was never uploaded (the confirm-upload existence check).
func (s *S3) Head(ctx context.Context, key string) (size int64, etag string, err error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return 0, "", ErrObjectNotFound
		}
		return 0, "", fmt.Errorf("heading %s: %w", key, err)
	}

	return aws.ToInt64(out.ContentLength), aws.ToString(out.ETag), nil
}

// Get streams the object's bytes (e.g. hashing at confirm time). The caller
// owns closing the reader.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("getting %s: %w", key, err)
	}

	return out.Body, nil
}

// Delete removes the object; deleting a missing key is not an error (S3
// semantics — keeps cleanup paths idempotent).
func (s *S3) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("deleting %s: %w", key, err)
	}
	return nil
}
