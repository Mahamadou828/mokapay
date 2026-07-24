package usecase_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/app/services/file/internal/business/file/usecase"
	coreaws "mokapay.app/core/aws"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/otel"
)

// captor records what the usecase publishes and proves the tx is supplied.
type captor struct {
	events []file.File
	sawTx  bool
}

func (c *captor) FileUploaded(_ context.Context, tx *sql.Tx, f file.File) error {
	c.sawTx = c.sawTx || tx != nil
	c.events = append(c.events, f)
	return nil
}

// Integration test: needs a migrated files database AND a MinIO. Skipped
// unless both TEST_DB_PORT and TEST_S3_ENDPOINT are set:
//
//	TEST_DB_PORT=5433 TEST_S3_ENDPOINT=http://localhost:9000 \
//	  go test ./internal/business/file/usecase/
func TestUploadLifecycle(t *testing.T) {
	dbPort, s3Endpoint := os.Getenv("TEST_DB_PORT"), os.Getenv("TEST_S3_ENDPOINT")
	if dbPort == "" || s3Endpoint == "" {
		t.Skip("TEST_DB_PORT and TEST_S3_ENDPOINT not set; integration test needs postgres + minio")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db := coredb.New(coredb.PostgresConfig{
		Host:     getenv("TEST_DB_HOST", "localhost"),
		Port:     dbPort,
		Username: getenv("TEST_DB_USER", "mokapay"),
		Password: getenv("TEST_DB_PASSWORD", "mokapay"),
		Database: getenv("TEST_DB_NAME", "files"),
	})
	if err := db.Open(); err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	storage, err := coreaws.NewS3(ctx, coreaws.S3Config{
		Endpoint:     s3Endpoint,
		Region:       "us-east-1",
		Bucket:       getenv("TEST_S3_BUCKET", "mokapay-files"),
		AccessKey:    getenv("TEST_S3_ACCESS_KEY", "mokapay"),
		SecretKey:    getenv("TEST_S3_SECRET_KEY", "mokapay1234"),
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}

	pub := &captor{}
	log := logger.New(os.Stdout, logger.LevelInfo, "file-test", otel.GetTraceID)
	uc := usecase.New(log, db, storage, pub, usecase.Config{
		Bucket:       getenv("TEST_S3_BUCKET", "mokapay-files"),
		UploadTTL:    time.Minute,
		DownloadTTL:  time.Minute,
		MaxSizeBytes: 1 << 20,
	})

	owner := uuid.New()
	body := []byte("proof of payment bytes")

	// Declaration validation.
	if _, _, _, err := uc.CreateUpload(ctx, newFile(owner, "text/plain", int64(len(body)))); !errors.Is(err, file.ErrContentTypeNotAllowed) {
		t.Fatalf("text/plain proof must be ErrContentTypeNotAllowed, got %v", err)
	}
	if _, _, _, err := uc.CreateUpload(ctx, newFile(owner, "image/png", 10<<20)); !errors.Is(err, file.ErrFileTooLarge) {
		t.Fatalf("oversize must be ErrFileTooLarge, got %v", err)
	}

	// Create → PUT → confirm.
	created, putURL, _, err := uc.CreateUpload(ctx, newFile(owner, "image/png", int64(len(body))))
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if created.Status != file.StatusPending {
		t.Fatalf("fresh upload status = %s, want pending", created.Status)
	}

	// Confirm before the PUT must fail on the missing object.
	if _, err := uc.ConfirmUpload(ctx, created.ID); !errors.Is(err, file.ErrObjectMissing) {
		t.Fatalf("confirm without upload must be ErrObjectMissing, got %v", err)
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building put request: %v", err)
	}
	putReq.Header.Set("Content-Type", "image/png")
	putRes, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	putRes.Body.Close()
	if putRes.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want 200", putRes.StatusCode)
	}

	confirmed, err := uc.ConfirmUpload(ctx, created.ID)
	if err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	wantHash := sha256.Sum256(body)
	if confirmed.SHA256 == nil || *confirmed.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("sha256 = %v, want %s", confirmed.SHA256, hex.EncodeToString(wantHash[:]))
	}
	if len(pub.events) != 1 || pub.events[0].ID != created.ID || !pub.sawTx {
		t.Fatalf("publish = %d events (sawTx=%v), want 1 in-tx event", len(pub.events), pub.sawTx)
	}

	// Double confirm loses the status guard.
	if _, err := uc.ConfirmUpload(ctx, created.ID); !errors.Is(err, file.ErrNotPending) {
		t.Fatalf("second confirm must be ErrNotPending, got %v", err)
	}

	// Read path: stored file gets a working presigned URL.
	got, url, _, err := uc.GetFile(ctx, created.ID)
	if err != nil || url == nil {
		t.Fatalf("GetFile = %v, url %v; want stored file with url", err, url)
	}
	if got.Status != file.StatusStored {
		t.Fatalf("status = %s, want stored", got.Status)
	}
	res, err := http.Get(*url)
	if err != nil {
		t.Fatalf("downloading: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", res.StatusCode)
	}

	// Listing by related entity finds it; reject flips it.
	files, err := uc.ListFiles(ctx, file.Filter{RelatedType: *created.RelatedType, RelatedID: *created.RelatedID})
	if err != nil || len(files) != 1 {
		t.Fatalf("ListFiles = %d, %v; want the one file", len(files), err)
	}
	rejected, err := uc.RejectFile(ctx, created.ID)
	if err != nil || rejected.Status != file.StatusRejected {
		t.Fatalf("RejectFile = %s, %v; want rejected", rejected.Status, err)
	}

	// Sweep ignores fresh pendings (the earlier failed-validation calls
	// never created rows; the confirmed row is not pending).
	if swept, err := uc.SweepExpired(ctx); err != nil || swept != 0 {
		t.Fatalf("SweepExpired = %d, %v; want 0", swept, err)
	}
}

func newFile(owner uuid.UUID, contentType string, size int64) file.NewFile {
	related := uuid.New()
	return file.NewFile{
		OwnerID:     owner,
		Purpose:     file.PurposeSenderProof,
		RelatedType: new("transfer"),
		RelatedID:   &related,
		FileName:    "proof.png",
		ContentType: contentType,
		SizeBytes:   size,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
