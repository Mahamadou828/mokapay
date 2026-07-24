package aws_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"mokapay.app/core/aws"
)

// Integration test: needs an S3-compatible store (the compose MinIO).
// Skipped unless TEST_S3_ENDPOINT is set, so `make test-all` stays green
// without infra:
//
//	TEST_S3_ENDPOINT=http://localhost:9000 go test ./core/aws/
func TestS3RoundTrip(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT not set; integration test needs MinIO/S3")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s3c, err := aws.NewS3(ctx, aws.S3Config{
		Endpoint:     endpoint,
		Region:       getenv("TEST_S3_REGION", "us-east-1"),
		Bucket:       getenv("TEST_S3_BUCKET", "mokapay-files"),
		AccessKey:    getenv("TEST_S3_ACCESS_KEY", "mokapay"),
		SecretKey:    getenv("TEST_S3_SECRET_KEY", "mokapay1234"),
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}

	key := fmt.Sprintf("test/%d", time.Now().UnixNano())
	body := []byte("mokapay s3 round trip")

	if _, _, err := s3c.Head(ctx, key); !errors.Is(err, aws.ErrObjectNotFound) {
		t.Fatalf("head before upload must be ErrObjectNotFound, got %v", err)
	}

	putURL, err := s3c.PresignPut(ctx, key, "text/plain", time.Minute)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building put request: %v", err)
	}
	putReq.Header.Set("Content-Type", "text/plain")
	putRes, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	defer putRes.Body.Close()
	if putRes.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want 200", putRes.StatusCode)
	}

	size, etag, err := s3c.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if size != int64(len(body)) || etag == "" {
		t.Fatalf("head = size %d etag %q, want size %d and an etag", size, etag, len(body))
	}

	getURL, err := s3c.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	getRes, err := http.Get(getURL)
	if err != nil {
		t.Fatalf("downloading: %v", err)
	}
	defer getRes.Body.Close()
	got, err := io.ReadAll(getRes.Body)
	if err != nil {
		t.Fatalf("reading download: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("downloaded %q, want %q", got, body)
	}

	if err := s3c.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s3c.Head(ctx, key); !errors.Is(err, aws.ErrObjectNotFound) {
		t.Fatalf("head after delete must be ErrObjectNotFound, got %v", err)
	}
	// Idempotent cleanup: deleting a gone key stays quiet.
	if err := s3c.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete must be a no-op, got %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
