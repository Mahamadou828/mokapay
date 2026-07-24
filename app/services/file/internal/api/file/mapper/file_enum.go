package mapper

import (
	"mokapay.app/app/services/file/internal/business/file"
	filev1 "mokapay.app/proto/codebase/file/v1"
)

func PurposeToProto(p file.Purpose) filev1.FilePurpose {
	switch p {
	case file.PurposeSenderProof:
		return filev1.FilePurpose_FILE_PURPOSE_SENDER_PROOF
	case file.PurposeAgentProof:
		return filev1.FilePurpose_FILE_PURPOSE_AGENT_PROOF
	case file.PurposeRecipientProof:
		return filev1.FilePurpose_FILE_PURPOSE_RECIPIENT_PROOF
	case file.PurposeDepositProof:
		return filev1.FilePurpose_FILE_PURPOSE_DEPOSIT_PROOF
	case file.PurposeKyc:
		return filev1.FilePurpose_FILE_PURPOSE_KYC
	case file.PurposeOther:
		return filev1.FilePurpose_FILE_PURPOSE_OTHER
	}
	return filev1.FilePurpose_FILE_PURPOSE_UNSPECIFIED
}

func PurposeFromProto(p filev1.FilePurpose) file.Purpose {
	switch p {
	case filev1.FilePurpose_FILE_PURPOSE_SENDER_PROOF:
		return file.PurposeSenderProof
	case filev1.FilePurpose_FILE_PURPOSE_AGENT_PROOF:
		return file.PurposeAgentProof
	case filev1.FilePurpose_FILE_PURPOSE_RECIPIENT_PROOF:
		return file.PurposeRecipientProof
	case filev1.FilePurpose_FILE_PURPOSE_DEPOSIT_PROOF:
		return file.PurposeDepositProof
	case filev1.FilePurpose_FILE_PURPOSE_KYC:
		return file.PurposeKyc
	case filev1.FilePurpose_FILE_PURPOSE_OTHER:
		return file.PurposeOther
	}
	return ""
}

func StatusToProto(s file.Status) filev1.FileStatus {
	switch s {
	case file.StatusPending:
		return filev1.FileStatus_FILE_STATUS_PENDING
	case file.StatusStored:
		return filev1.FileStatus_FILE_STATUS_STORED
	case file.StatusRejected:
		return filev1.FileStatus_FILE_STATUS_REJECTED
	}
	return filev1.FileStatus_FILE_STATUS_UNSPECIFIED
}
