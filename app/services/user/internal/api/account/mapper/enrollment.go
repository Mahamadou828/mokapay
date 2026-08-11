package mapper

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/user/internal/business/enrollment"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func EnrollmentToProto(in enrollment.Enrollment) *userv1.Enrollment {
	out := &userv1.Enrollment{
		Id:         in.ID.String(),
		AccountId:  in.AccountID.String(),
		Status:     EnrollmentStatusToProto(in.Status),
		TosVersion: in.TOSVersion,
		CreatedAt:  timestamppb.New(in.CreatedAt),
		UpdatedAt:  timestamppb.New(in.UpdatedAt),
	}
	// Absent on a self-serve request — the client reads that absence to
	// tell the two flows apart.
	if in.InvitedBy != nil {
		out.InvitedBy = new(in.InvitedBy.String())
	}
	if in.TOSAcceptedAt != nil {
		out.TosAcceptedAt = timestamppb.New(*in.TOSAcceptedAt)
	}
	if in.DecidedBy != nil {
		out.DecidedBy = new(in.DecidedBy.String())
	}
	if in.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*in.DecidedAt)
	}

	return out
}

// SelfEnrollToDomain maps the AG-04 submission. City stays a pointer all
// the way down: "not provided" and "empty" are the same absence, and the
// payment service treats a missing city as "no geo hint".
func SelfEnrollToDomain(in *userv1.SelfEnrollRequest) enrollment.SelfEnrollment {
	out := enrollment.SelfEnrollment{TOSVersion: in.TosVersion}
	if in.City != nil && *in.City != "" {
		out.City = new(*in.City)
	}
	return out
}

func EnrollmentStatusToProto(in enrollment.Status) userv1.EnrollmentStatus {
	switch in {
	case enrollment.StatusInvited:
		return userv1.EnrollmentStatus_ENROLLMENT_STATUS_INVITED
	case enrollment.StatusAccepted:
		return userv1.EnrollmentStatus_ENROLLMENT_STATUS_ACCEPTED
	case enrollment.StatusDeclined:
		return userv1.EnrollmentStatus_ENROLLMENT_STATUS_DECLINED
	case enrollment.StatusApproved:
		return userv1.EnrollmentStatus_ENROLLMENT_STATUS_APPROVED
	case enrollment.StatusCancelled:
		return userv1.EnrollmentStatus_ENROLLMENT_STATUS_CANCELLED
	}
	return userv1.EnrollmentStatus_ENROLLMENT_STATUS_UNSPECIFIED
}

// EnrollmentStatusFromProto returns nil for UNSPECIFIED — "not filtering".
func EnrollmentStatusFromProto(in userv1.EnrollmentStatus) *enrollment.Status {
	switch in {
	case userv1.EnrollmentStatus_ENROLLMENT_STATUS_INVITED:
		return new(enrollment.StatusInvited)
	case userv1.EnrollmentStatus_ENROLLMENT_STATUS_ACCEPTED:
		return new(enrollment.StatusAccepted)
	case userv1.EnrollmentStatus_ENROLLMENT_STATUS_DECLINED:
		return new(enrollment.StatusDeclined)
	case userv1.EnrollmentStatus_ENROLLMENT_STATUS_APPROVED:
		return new(enrollment.StatusApproved)
	case userv1.EnrollmentStatus_ENROLLMENT_STATUS_CANCELLED:
		return new(enrollment.StatusCancelled)
	}
	return nil
}
