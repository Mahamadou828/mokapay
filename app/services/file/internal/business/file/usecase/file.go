package usecase

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/app/services/file/internal/business/file/repo"
	coreaws "mokapay.app/core/aws"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Storage is the object-store slice this domain needs (satisfied by
// *core/aws.S3).
type Storage interface {
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Head(ctx context.Context, key string) (size int64, etag string, err error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// Publisher announces a stored file to its consumers. The tx is the confirm
// transaction: implementations must enlist in it (River InsertTx) so the
// status flip and the event commit or roll back together.
type Publisher interface {
	FileUploaded(ctx context.Context, tx *sql.Tx, f file.File) error
}

// Config carries the upload policy knobs.
type Config struct {
	Bucket       string
	UploadTTL    time.Duration // presigned PUT lifetime
	DownloadTTL  time.Duration // presigned GET lifetime
	MaxSizeBytes int64
}

type UseCase struct {
	logger    *logger.Logger
	db        *coredb.Postgres
	repo      repo.Store
	storage   Storage
	publisher Publisher
	cfg       Config
}

func New(l *logger.Logger, db *coredb.Postgres, storage Storage, publisher Publisher, cfg Config) UseCase {
	return UseCase{
		logger:    l,
		db:        db,
		repo:      repo.New(db),
		storage:   storage,
		publisher: publisher,
		cfg:       cfg,
	}
}

// allowedContentTypes per purpose; nil means anything goes.
var allowedContentTypes = map[file.Purpose][]string{
	file.PurposeSenderProof:    proofTypes,
	file.PurposeAgentProof:     proofTypes,
	file.PurposeRecipientProof: proofTypes,
	file.PurposeDepositProof:   proofTypes,
	file.PurposeKyc:            proofTypes,
	file.PurposeOther:          nil,
}

var proofTypes = []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}

// CreateUpload validates the declaration, registers the pending file and
// returns the presigned PUT URL the client uploads the bytes to.
func (u UseCase) CreateUpload(ctx context.Context, nf file.NewFile) (file.File, string, time.Duration, error) {
	if err := u.validate(nf); err != nil {
		return file.File{}, "", 0, err
	}

	// Server-assigned key: client names never reach storage.
	storageKey := fmt.Sprintf("%s/%s", nf.Purpose, uuid.NewString())

	created, err := u.repo.AddFile(ctx, nf, u.cfg.Bucket, storageKey)
	if err != nil {
		u.logger.Error(ctx, "File.CreateUpload: failed to add file", "err", err)
		return file.File{}, "", 0, err
	}

	url, err := u.storage.PresignPut(ctx, storageKey, nf.ContentType, u.cfg.UploadTTL)
	if err != nil {
		// The pending row stays; the sweeper collects it after the TTL.
		u.logger.Error(ctx, "File.CreateUpload: failed to presign put", "err", err)
		return file.File{}, "", 0, fmt.Errorf("presigning upload: %w", err)
	}

	return created, url, u.cfg.UploadTTL, nil
}

// ConfirmUpload verifies the object landed in storage, hashes it, marks the
// file stored and announces it — the status flip and the event are one
// transaction.
func (u UseCase) ConfirmUpload(ctx context.Context, id uuid.UUID) (file.File, error) {
	pending, err := u.repo.GetFile(ctx, id.String())
	if err != nil {
		return file.File{}, err
	}
	if pending.Status != file.StatusPending {
		return file.File{}, file.ErrNotPending
	}

	size, etag, err := u.storage.Head(ctx, pending.StorageKey)
	if err != nil {
		if errors.Is(err, coreaws.ErrObjectNotFound) {
			return file.File{}, file.ErrObjectMissing
		}
		u.logger.Error(ctx, "File.ConfirmUpload: failed to head object", "err", err)
		return file.File{}, fmt.Errorf("checking object: %w", err)
	}
	if size != pending.SizeBytes {
		return file.File{}, file.ErrSizeMismatch
	}

	hash, err := u.hashObject(ctx, pending.StorageKey)
	if err != nil {
		u.logger.Error(ctx, "File.ConfirmUpload: failed to hash object", "err", err)
		return file.File{}, fmt.Errorf("hashing object: %w", err)
	}

	var confirmed file.File
	if err := u.db.Begin(ctx, func(tx *sql.Tx) error {
		confirmed, err = u.repo.WithTx(tx).ConfirmFile(ctx, id.String(), hash, etag)
		if err != nil {
			return err
		}
		return u.publisher.FileUploaded(ctx, tx, confirmed)
	}); err != nil {
		if errors.Is(err, file.ErrNotPending) {
			return file.File{}, file.ErrNotPending
		}
		u.logger.Error(ctx, "File.ConfirmUpload: failed to confirm", "err", err)
		return file.File{}, fmt.Errorf("confirming upload: %w", err)
	}

	return confirmed, nil
}

// GetFile returns the metadata plus, for stored files, a presigned read URL.
func (u UseCase) GetFile(ctx context.Context, id uuid.UUID) (file.File, *string, time.Duration, error) {
	f, err := u.repo.GetFile(ctx, id.String())
	if err != nil {
		return file.File{}, nil, 0, err
	}

	if f.Status != file.StatusStored {
		return f, nil, 0, nil
	}

	url, err := u.storage.PresignGet(ctx, f.StorageKey, u.cfg.DownloadTTL)
	if err != nil {
		u.logger.Error(ctx, "File.GetFile: failed to presign get", "err", err)
		return file.File{}, nil, 0, fmt.Errorf("presigning download: %w", err)
	}

	return f, new(url), u.cfg.DownloadTTL, nil
}

func (u UseCase) ListFiles(ctx context.Context, f file.Filter) ([]file.File, error) {
	return u.repo.ListFiles(ctx, f)
}

func (u UseCase) RejectFile(ctx context.Context, id uuid.UUID) (file.File, error) {
	return u.repo.RejectFile(ctx, id.String())
}

// SweepExpired cancels pending uploads whose presign TTL (+grace) has
// passed: the half-uploaded object (if any) is deleted, the row is
// soft-deleted. Returns how many were swept. Runs as a River periodic job
// once that integration lands.
func (u UseCase) SweepExpired(ctx context.Context) (int, error) {
	// Grace on top of the TTL: a client may still be PUTting right at expiry.
	cutoff := time.Now().Add(-(u.cfg.UploadTTL + 5*time.Minute))

	expired, err := u.repo.ListExpiredPending(ctx, cutoff, 100)
	if err != nil {
		return 0, err
	}

	swept := 0
	for _, f := range expired {
		if err := u.storage.Delete(ctx, f.StorageKey); err != nil {
			u.logger.Error(ctx, "File.SweepExpired: failed to delete object — will retry next run", "err", err, "file", f.ID)
			continue
		}
		if err := u.repo.DeleteFile(ctx, f.ID.String()); err != nil {
			u.logger.Error(ctx, "File.SweepExpired: failed to delete row", "err", err, "file", f.ID)
			continue
		}
		swept++
	}

	return swept, nil
}

func (u UseCase) validate(nf file.NewFile) error {
	if _, ok := allowedContentTypes[nf.Purpose]; !ok {
		return file.ErrInvalidPurpose
	}
	if nf.SizeBytes <= 0 || nf.SizeBytes > u.cfg.MaxSizeBytes {
		return file.ErrFileTooLarge
	}

	allowed := allowedContentTypes[nf.Purpose]
	if allowed == nil {
		return nil
	}
	for _, ct := range allowed {
		if nf.ContentType == ct {
			return nil
		}
	}
	return file.ErrContentTypeNotAllowed
}

// hashObject streams the stored object through sha256; size is bounded by
// the upload policy, re-checked here so a swapped object can't balloon.
func (u UseCase) hashObject(ctx context.Context, key string) (string, error) {
	body, err := u.storage.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer body.Close()

	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(body, u.cfg.MaxSizeBytes+1)); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
