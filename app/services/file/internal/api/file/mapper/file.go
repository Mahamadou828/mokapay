// Package mapper converts between the file business types and their proto
// counterparts.
package mapper

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/file/internal/business/file"
	filev1 "mokapay.app/proto/codebase/file/v1"
)

func FileToProto(in file.File) *filev1.File {
	out := &filev1.File{
		Id:          in.ID.String(),
		OwnerId:     in.OwnerID.String(),
		Purpose:     PurposeToProto(in.Purpose),
		RelatedType: in.RelatedType,
		FileName:    in.FileName,
		ContentType: in.ContentType,
		SizeBytes:   in.SizeBytes,
		Sha256:      in.SHA256,
		Status:      StatusToProto(in.Status),
		CreatedAt:   timestamppb.New(in.CreatedAt),
		UpdatedAt:   timestamppb.New(in.UpdatedAt),
	}
	if in.RelatedID != nil {
		out.RelatedId = new(in.RelatedID.String())
	}

	return out
}

// CreateUploadToDomain maps the declaration; the owner comes from the
// caller's passport, never from the request.
func CreateUploadToDomain(ownerID uuid.UUID, in *filev1.CreateUploadRequest, relatedID *uuid.UUID) file.NewFile {
	return file.NewFile{
		OwnerID:     ownerID,
		Purpose:     PurposeFromProto(in.Purpose),
		RelatedType: in.RelatedType,
		RelatedID:   relatedID,
		FileName:    in.FileName,
		ContentType: in.ContentType,
		SizeBytes:   in.SizeBytes,
	}
}

func FilterToDomain(in *filev1.ListFilesRequest, relatedID uuid.UUID) file.Filter {
	out := file.Filter{
		RelatedType: in.RelatedType,
		RelatedID:   relatedID,
	}
	if in.Purpose != nil {
		out.Purpose = new(PurposeFromProto(*in.Purpose))
	}

	return out
}
