package file

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/file/internal/api/file/mapper"
	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/app/services/file/internal/sdk/grpcerr"
	"mokapay.app/core/passport"
	filev1 "mokapay.app/proto/codebase/file/v1"
)

func (h Handler) CreateUpload(ctx context.Context, req *filev1.CreateUploadRequest) (*filev1.CreateUploadResponse, error) {
	owner, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	switch {
	case req.Purpose == filev1.FilePurpose_FILE_PURPOSE_UNSPECIFIED:
		return nil, status.Error(codes.InvalidArgument, "purpose is required")
	case req.FileName == "":
		return nil, status.Error(codes.InvalidArgument, "file_name is required")
	case req.ContentType == "":
		return nil, status.Error(codes.InvalidArgument, "content_type is required")
	case req.SizeBytes <= 0:
		return nil, status.Error(codes.InvalidArgument, "size_bytes is required")
	case (req.RelatedType == nil) != (req.RelatedId == nil):
		return nil, status.Error(codes.InvalidArgument, "related_type and related_id go together")
	}

	relatedID, err := parseOptionalID(req.RelatedId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid related_id")
	}

	created, url, ttl, err := h.uc.CreateUpload(ctx, mapper.CreateUploadToDomain(owner, req, relatedID))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return &filev1.CreateUploadResponse{
		File:               mapper.FileToProto(created),
		UploadUrl:          url,
		UploadUrlExpiresIn: int64(ttl.Seconds()),
	}, nil
}

func (h Handler) ConfirmUpload(ctx context.Context, req *filev1.ConfirmUploadRequest) (*filev1.File, error) {
	caller, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid file id")
	}

	// Only the uploader (or ops) confirms an upload.
	pending, _, _, err := h.uc.GetFile(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}
	if pending.OwnerID != caller && !isOps(ctx) {
		return nil, status.Error(codes.PermissionDenied, "not your file")
	}

	confirmed, err := h.uc.ConfirmUpload(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.FileToProto(confirmed), nil
}

func (h Handler) GetFile(ctx context.Context, req *filev1.GetFileRequest) (*filev1.GetFileResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid file id")
	}

	f, url, ttl, err := h.uc.GetFile(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}
	if err := authorizeRead(ctx, f); err != nil {
		return nil, err
	}

	out := &filev1.GetFileResponse{File: mapper.FileToProto(f)}
	if url != nil {
		out.DownloadUrl = url
		out.DownloadUrlExpiresIn = new(int64(ttl.Seconds()))
	}

	return out, nil
}

func (h Handler) ListFiles(ctx context.Context, req *filev1.ListFilesRequest) (*filev1.ListFilesResponse, error) {
	if req.RelatedType == "" {
		return nil, status.Error(codes.InvalidArgument, "related_type is required")
	}
	relatedID, err := uuid.Parse(req.RelatedId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid related_id")
	}

	// Reads on a related entity: service-internal calls (no passport) and
	// ops see everything; an end user only lists files they own.
	files, err := h.uc.ListFiles(ctx, mapper.FilterToDomain(req, relatedID))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*filev1.File, 0, len(files))
	for _, f := range files {
		if err := authorizeRead(ctx, f); err != nil {
			continue // skip rows the caller may not see
		}
		out = append(out, mapper.FileToProto(f))
	}

	return &filev1.ListFilesResponse{Files: out}, nil
}

func (h Handler) RejectFile(ctx context.Context, req *filev1.RejectFileRequest) (*filev1.File, error) {
	if !isOps(ctx) {
		return nil, status.Error(codes.PermissionDenied, "ops role required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid file id")
	}

	rejected, err := h.uc.RejectFile(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.FileToProto(rejected), nil
}

// callerID resolves the authenticated account from the passport; writes
// always require one.
func callerID(ctx context.Context) (uuid.UUID, error) {
	p, err := passport.FromContext(ctx)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "missing caller identity")
	}

	id, err := uuid.Parse(p.User.ID)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "invalid caller identity")
	}

	return id, nil
}

// authorizeRead: no passport = trusted service-internal call; otherwise the
// caller must own the file or hold an ops-tier role (design doc F2).
func authorizeRead(ctx context.Context, f file.File) error {
	p, err := passport.FromContext(ctx)
	if err != nil || isOps(ctx) || p.User.ID == f.OwnerID.String() {
		return nil
	}
	return status.Error(codes.PermissionDenied, "not your file")
}

func isOps(ctx context.Context) bool {
	p, err := passport.FromContext(ctx)
	return err == nil && (p.User.Role == "ops" || p.User.Role == "admin" || p.Type == passport.TypeSystem)
}

func parseOptionalID(s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
