package mapper

import (
	"mokapay.app/app/services/user/internal/business/auth"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func OtpPurposeFromProto(p userv1.OtpPurpose) auth.OtpPurpose {
	switch p {
	case userv1.OtpPurpose_OTP_PURPOSE_REGISTER:
		return auth.OtpPurposeRegister
	case userv1.OtpPurpose_OTP_PURPOSE_LOGIN:
		return auth.OtpPurposeLogin
	case userv1.OtpPurpose_OTP_PURPOSE_RESET:
		return auth.OtpPurposeReset
	}
	return ""
}

func LanguageFromProto(l userv1.Language) auth.Language {
	switch l {
	case userv1.Language_LANGUAGE_FR:
		return auth.LanguageFr
	case userv1.Language_LANGUAGE_EN:
		return auth.LanguageEn
	}
	return "" // store default applies
}
