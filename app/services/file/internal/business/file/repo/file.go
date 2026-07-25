// Package repo owns the file domain's persistence: the files table. Reads
// go through the files_ext view, writes through the files_ext procedures.
// The object bytes are not this layer's concern.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/app/services/file/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction — used to commit
// the confirm status flip atomically with the event enqueue.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// AddFile registers a pending upload. Bucket and storage key are assigned
// by the usecase, never by the caller.
func (s Store) AddFile(ctx context.Context, nf file.NewFile, bucket, storageKey string) (file.File, error) {
	row, err := s.sqlc.AddFile(ctx, sqlc.AddFileParams{
		OwnerNk:     nf.OwnerID.String(),
		Purpose:     sqlc.FilePurpose(nf.Purpose),
		Bucket:      bucket,
		StorageKey:  storageKey,
		FileName:    nf.FileName,
		ContentType: nf.ContentType,
		SizeBytes:   nf.SizeBytes,
		RelatedType: toNullString(nf.RelatedType),
		RelatedNk:   toNullUUID(nf.RelatedID),
	})
	if err != nil {
		return file.File{}, fmt.Errorf("adding file: %w", err)
	}

	return fileToDomain(row)
}

// GetFile returns the live file with the given id.
func (s Store) GetFile(ctx context.Context, id string) (file.File, error) {
	row, err := s.sqlc.GetFile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return file.File{}, file.ErrFileNotFound
		}
		return file.File{}, fmt.Errorf("getting file %s: %w", id, err)
	}

	return viewFileToDomain(row)
}

// ListFiles returns the live files of one related entity, newest first.
func (s Store) ListFiles(ctx context.Context, f file.Filter) ([]file.File, error) {
	rows, err := s.sqlc.ListFiles(ctx, sqlc.ListFilesParams{
		// The column is nullable so sqlc types the arg as a null-string;
		// the filter itself is always set.
		RelatedType: pgtype.Text{String: f.RelatedType, Valid: true},
		RelatedNk:   f.RelatedID.String(),
		Purpose:     toNullPurpose(f.Purpose),
	})
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}

	out := make([]file.File, 0, len(rows))
	for _, row := range rows {
		fl, err := viewFileToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, fl)
	}

	return out, nil
}

// ListExpiredPending returns pending uploads created before the cutoff —
// the orphan-sweep worklist.
func (s Store) ListExpiredPending(ctx context.Context, before time.Time, limit int32) ([]file.File, error) {
	rows, err := s.sqlc.ListExpiredPendingFiles(ctx, sqlc.ListExpiredPendingFilesParams{
		Before:  before,
		MaxRows: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing expired pending files: %w", err)
	}

	out := make([]file.File, 0, len(rows))
	for _, row := range rows {
		fl, err := viewFileToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, fl)
	}

	return out, nil
}

// ConfirmFile atomically flips a pending upload to stored, recording what
// landed in object storage. file.ErrNotPending means a parallel confirm
// won, or the row was rejected/swept meanwhile.
func (s Store) ConfirmFile(ctx context.Context, id, sha256, etag string) (file.File, error) {
	row, err := s.sqlc.ConfirmFile(ctx, sqlc.ConfirmFileParams{
		ID:     id,
		Sha256: sha256,
		Etag:   etag,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return file.File{}, file.ErrNotPending
		}
		return file.File{}, fmt.Errorf("confirming file %s: %w", id, err)
	}

	return fileToDomain(row)
}

// RejectFile flags a stored file as rejected; the row stays for audit.
func (s Store) RejectFile(ctx context.Context, id string) (file.File, error) {
	row, err := s.sqlc.RejectFile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return file.File{}, file.ErrNotStored
		}
		return file.File{}, fmt.Errorf("rejecting file %s: %w", id, err)
	}

	return fileToDomain(row)
}

// DeleteFile soft-deletes the row; deleting an unknown or already-deleted
// id is a no-op.
func (s Store) DeleteFile(ctx context.Context, id string) error {
	if err := s.sqlc.DeleteFile(ctx, id); err != nil {
		return fmt.Errorf("deleting file %s: %w", id, err)
	}
	return nil
}

// fileToDomain converts a base-table row (write procedures return files).
func fileToDomain(f sqlc.File) (file.File, error) {
	return toDomain(f.ID, f.OwnerNk, f.Purpose, f.RelatedType, f.RelatedNk, f.Bucket,
		f.StorageKey, f.FileName, f.ContentType, f.SizeBytes, f.Sha256, f.Etag,
		f.Status, f.CreatedAt, f.UpdatedAt)
}

// viewFileToDomain converts a files_ext.files row.
func viewFileToDomain(f sqlc.FilesExtFile) (file.File, error) {
	return toDomain(f.ID, f.OwnerNk, f.Purpose, f.RelatedType, f.RelatedNk, f.Bucket,
		f.StorageKey, f.FileName, f.ContentType, f.SizeBytes, f.Sha256, f.Etag,
		f.Status, f.CreatedAt, f.UpdatedAt)
}

func toDomain(
	id, ownerNk string, purpose sqlc.FilePurpose,
	relatedType pgtype.Text, relatedNk uuid.NullUUID,
	bucket, storageKey, fileName, contentType string, sizeBytes int64,
	sha256, etag pgtype.Text, status sqlc.FileStatus,
	createdAt, updatedAt time.Time,
) (file.File, error) {
	fid, err := uuid.Parse(id)
	if err != nil {
		return file.File{}, fmt.Errorf("parsing file id %q: %w", id, err)
	}
	owner, err := uuid.Parse(ownerNk)
	if err != nil {
		return file.File{}, fmt.Errorf("parsing owner id %q: %w", ownerNk, err)
	}

	out := file.File{
		ID:          fid,
		OwnerID:     owner,
		Purpose:     file.Purpose(purpose),
		Bucket:      bucket,
		StorageKey:  storageKey,
		FileName:    fileName,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		Status:      file.Status(status),
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	if relatedType.Valid {
		out.RelatedType = new(relatedType.String)
	}
	if relatedNk.Valid {
		out.RelatedID = new(relatedNk.UUID)
	}
	if sha256.Valid {
		out.SHA256 = new(sha256.String)
	}
	if etag.Valid {
		out.ETag = new(etag.String)
	}

	return out, nil
}

func toNullString(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func toNullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func toNullPurpose(p *file.Purpose) sqlc.NullFilePurpose {
	if p == nil {
		return sqlc.NullFilePurpose{}
	}
	return sqlc.NullFilePurpose{FilePurpose: sqlc.FilePurpose(*p), Valid: true}
}
