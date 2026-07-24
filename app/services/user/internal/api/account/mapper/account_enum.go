package mapper

import (
	"mokapay.app/app/services/user/internal/business/auth"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func AccountRoleToProto(r auth.AccountRole) userv1.AccountRole {
	switch r {
	case auth.AccountRoleUser:
		return userv1.AccountRole_ACCOUNT_ROLE_USER
	case auth.AccountRolePendingAgent:
		return userv1.AccountRole_ACCOUNT_ROLE_PENDING_AGENT
	case auth.AccountRoleAgent:
		return userv1.AccountRole_ACCOUNT_ROLE_AGENT
	case auth.AccountRoleOps:
		return userv1.AccountRole_ACCOUNT_ROLE_OPS
	case auth.AccountRoleAdmin:
		return userv1.AccountRole_ACCOUNT_ROLE_ADMIN
	}
	return userv1.AccountRole_ACCOUNT_ROLE_UNSPECIFIED
}

func AccountRoleFromProto(r userv1.AccountRole) auth.AccountRole {
	switch r {
	case userv1.AccountRole_ACCOUNT_ROLE_USER:
		return auth.AccountRoleUser
	case userv1.AccountRole_ACCOUNT_ROLE_PENDING_AGENT:
		return auth.AccountRolePendingAgent
	case userv1.AccountRole_ACCOUNT_ROLE_AGENT:
		return auth.AccountRoleAgent
	case userv1.AccountRole_ACCOUNT_ROLE_OPS:
		return auth.AccountRoleOps
	case userv1.AccountRole_ACCOUNT_ROLE_ADMIN:
		return auth.AccountRoleAdmin
	}
	return ""
}

func AccountStatusToProto(s auth.AccountStatus) userv1.AccountStatus {
	switch s {
	case auth.AccountStatusPending:
		return userv1.AccountStatus_ACCOUNT_STATUS_PENDING
	case auth.AccountStatusActive:
		return userv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case auth.AccountStatusSuspended:
		return userv1.AccountStatus_ACCOUNT_STATUS_SUSPENDED
	case auth.AccountStatusBanned:
		return userv1.AccountStatus_ACCOUNT_STATUS_BANNED
	}
	return userv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
}

func AccountStatusFromProto(s userv1.AccountStatus) auth.AccountStatus {
	switch s {
	case userv1.AccountStatus_ACCOUNT_STATUS_PENDING:
		return auth.AccountStatusPending
	case userv1.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return auth.AccountStatusActive
	case userv1.AccountStatus_ACCOUNT_STATUS_SUSPENDED:
		return auth.AccountStatusSuspended
	case userv1.AccountStatus_ACCOUNT_STATUS_BANNED:
		return auth.AccountStatusBanned
	}
	return ""
}

func LanguageToProto(l auth.Language) userv1.Language {
	switch l {
	case auth.LanguageFr:
		return userv1.Language_LANGUAGE_FR
	case auth.LanguageEn:
		return userv1.Language_LANGUAGE_EN
	}
	return userv1.Language_LANGUAGE_UNSPECIFIED
}

func LanguageFromProto(l userv1.Language) auth.Language {
	switch l {
	case userv1.Language_LANGUAGE_FR:
		return auth.LanguageFr
	case userv1.Language_LANGUAGE_EN:
		return auth.LanguageEn
	}
	return ""
}
