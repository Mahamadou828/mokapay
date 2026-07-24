// Package mapper converts between the account business types and their proto
// counterparts, shared by the api/account (UserService) and api/admin
// (AdminService) handlers.
package mapper

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/user/internal/business/auth"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func AccountToProto(in auth.Account) *userv1.Account {
	out := &userv1.Account{
		Id:                in.ID.String(),
		PhoneNumber:       in.PhoneNumber,
		FirstName:         in.FirstName,
		LastName:          in.LastName,
		Email:             in.Email,
		PreferredLanguage: LanguageToProto(in.PreferredLanguage),
		Role:              AccountRoleToProto(in.Role),
		Status:            AccountStatusToProto(in.Status),
		CreatedAt:         timestamppb.New(in.CreatedAt),
		UpdatedAt:         timestamppb.New(in.UpdatedAt),
	}
	if in.LastLoginAt != nil {
		out.LastLoginAt = timestamppb.New(*in.LastLoginAt)
	}

	return out
}

// UpdateOwnToDomain maps a self-service update: profile fields only — Role
// and Status stay nil no matter what, and the ID is resolved from the caller
// by the usecase.
func UpdateOwnToDomain(in *userv1.UpdateAccountRequest) auth.UpdateAccount {
	out := auth.UpdateAccount{
		FirstName: in.FirstName,
		LastName:  in.LastName,
		Email:     in.Email,
	}
	if in.PreferredLanguage != nil {
		out.PreferredLanguage = new(LanguageFromProto(*in.PreferredLanguage))
	}

	return out
}

// AdminUpdateToDomain maps a moderation update: profile fields plus role and
// status.
func AdminUpdateToDomain(id uuid.UUID, in *userv1.AdminServiceUpdateAccountRequest) auth.UpdateAccount {
	out := auth.UpdateAccount{
		ID:        id,
		FirstName: in.FirstName,
		LastName:  in.LastName,
		Email:     in.Email,
	}
	if in.PreferredLanguage != nil {
		out.PreferredLanguage = new(LanguageFromProto(*in.PreferredLanguage))
	}
	if in.Role != nil {
		out.Role = new(AccountRoleFromProto(*in.Role))
	}
	if in.Status != nil {
		out.Status = new(AccountStatusFromProto(*in.Status))
	}

	return out
}

func AccountFilterToDomain(in *userv1.ListAccountsRequest) auth.AccountFilter {
	var out auth.AccountFilter
	if in.Role != nil {
		out.Role = new(AccountRoleFromProto(*in.Role))
	}
	if in.Status != nil {
		out.Status = new(AccountStatusFromProto(*in.Status))
	}
	out.PhoneNumber = in.PhoneNumber

	return out
}
