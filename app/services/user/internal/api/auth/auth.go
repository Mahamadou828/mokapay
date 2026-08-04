package auth

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/user/internal/api/auth/mapper"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func (h Handler) SendOtp(ctx context.Context, req *userv1.SendOtpRequest) (*userv1.SendOtpResponse, error) {
	if req.PhoneNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "phone_number is required")
	}
	if req.Purpose == userv1.OtpPurpose_OTP_PURPOSE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "purpose is required")
	}

	expiresAt, err := h.uc.SendOTP(ctx, req.PhoneNumber, mapper.OtpPurposeFromProto(req.Purpose))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return &userv1.SendOtpResponse{ExpiresInSeconds: mapper.SecondsUntil(expiresAt)}, nil
}

// ValidateOtp checks a code without consuming it — the client calls it at the
// code-entry step so a wrong code fails there, not after the user has filled
// in the rest of the flow.
func (h Handler) ValidateOtp(ctx context.Context, req *userv1.ValidateOtpRequest) (*emptypb.Empty, error) {
	switch {
	case req.PhoneNumber == "":
		return nil, status.Error(codes.InvalidArgument, "phone_number is required")
	case req.Code == "":
		return nil, status.Error(codes.InvalidArgument, "code is required")
	case req.Purpose == userv1.OtpPurpose_OTP_PURPOSE_UNSPECIFIED:
		return nil, status.Error(codes.InvalidArgument, "purpose is required")
	}

	if err := h.uc.ValidateOTP(ctx, req.PhoneNumber, req.Code, mapper.OtpPurposeFromProto(req.Purpose)); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h Handler) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.AuthResponse, error) {
	switch {
	case req.PhoneNumber == "":
		return nil, status.Error(codes.InvalidArgument, "phone_number is required")
	case req.Code == "":
		return nil, status.Error(codes.InvalidArgument, "code is required")
	case req.Password == "":
		return nil, status.Error(codes.InvalidArgument, "password is required")
	case req.FirstName == "":
		return nil, status.Error(codes.InvalidArgument, "first_name is required")
	case req.LastName == "":
		return nil, status.Error(codes.InvalidArgument, "last_name is required")
	}

	token, err := h.uc.RegisterUser(ctx, mapper.RegisterToDomain(req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TokenToAuthResponse(token), nil
}

func (h Handler) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.AuthResponse, error) {
	switch {
	case req.PhoneNumber == "":
		return nil, status.Error(codes.InvalidArgument, "phone_number is required")
	case req.Password == "":
		return nil, status.Error(codes.InvalidArgument, "password is required")
	case req.Code == "":
		return nil, status.Error(codes.InvalidArgument, "code is required")
	}

	token, err := h.uc.Login(ctx, req.PhoneNumber, req.Password, req.Code)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TokenToAuthResponse(token), nil
}

func (h Handler) RefreshToken(ctx context.Context, req *userv1.RefreshTokenRequest) (*userv1.AuthResponse, error) {
	switch {
	case req.RefreshToken == "":
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	case req.Sub == "":
		return nil, status.Error(codes.InvalidArgument, "sub is required")
	}

	token, err := h.uc.RefreshToken(ctx, req.RefreshToken, req.Sub)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TokenToAuthResponse(token), nil
}

func (h Handler) Logout(ctx context.Context, _ *userv1.LogoutRequest) (*emptypb.Empty, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	if err := h.uc.LogOut(ctx, pass.Claim.Subject); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h Handler) ChangePassword(ctx context.Context, req *userv1.ChangePasswordRequest) (*emptypb.Empty, error) {
	switch {
	case req.PhoneNumber == "":
		return nil, status.Error(codes.InvalidArgument, "phone_number is required")
	case req.Code == "":
		return nil, status.Error(codes.InvalidArgument, "code is required")
	case req.NewPassword == "":
		return nil, status.Error(codes.InvalidArgument, "new_password is required")
	}

	if err := h.uc.ChangePassword(ctx, req.PhoneNumber, req.Code, req.NewPassword); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}
