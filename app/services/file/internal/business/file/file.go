package file

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrFileNotFound = errors.New("file not found")

// ErrNotPending is returned when confirming an upload that is no longer
// pending — double confirm, rejected, or swept meanwhile.
var ErrNotPending = errors.New("upload not pending")

// ErrNotStored is returned when rejecting a file that is not stored.
var ErrNotStored = errors.New("file not stored")

// ErrObjectMissing is returned at confirm when nothing was uploaded to the
// presigned URL.
var ErrObjectMissing = errors.New("object not uploaded")

// ErrSizeMismatch is returned at confirm when the stored object's size
// differs from the declared one.
var ErrSizeMismatch = errors.New("uploaded size does not match declaration")

var ErrInvalidPurpose = errors.New("invalid purpose")
var ErrContentTypeNotAllowed = errors.New("content type not allowed for this purpose")
var ErrFileTooLarge = errors.New("file exceeds the size limit")

// Purpose values mirror the stored file_purpose enum.
type Purpose string

const (
	PurposeSenderProof    Purpose = "sender_proof"
	PurposeAgentProof     Purpose = "agent_proof"
	PurposeRecipientProof Purpose = "recipient_proof"
	PurposeDepositProof   Purpose = "deposit_proof"
	PurposeKyc            Purpose = "kyc"
	PurposeOther          Purpose = "other"
)

// Status values mirror the stored file_status enum.
type Status string

const (
	StatusPending  Status = "pending"
	StatusStored   Status = "stored"
	StatusRejected Status = "rejected"
)

// File is the read entity: metadata only — the bytes live in object storage
// and are reachable exclusively through presigned URLs.
type File struct {
	ID          uuid.UUID
	OwnerID     uuid.UUID
	Purpose     Purpose
	RelatedType *string    // nil when the file is unattached
	RelatedID   *uuid.UUID // id owned by another service; loose reference
	Bucket      string
	StorageKey  string
	FileName    string
	ContentType string
	SizeBytes   int64
	SHA256      *string // set once stored
	ETag        *string
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewFile carries everything the caller declares about an upload; the
// service assigns bucket and storage key.
type NewFile struct {
	OwnerID     uuid.UUID
	Purpose     Purpose
	RelatedType *string
	RelatedID   *uuid.UUID
	FileName    string
	ContentType string
	SizeBytes   int64
}

// Filter narrows file listings to one related entity, optionally one purpose.
type Filter struct {
	RelatedType string
	RelatedID   uuid.UUID
	Purpose     *Purpose
}
