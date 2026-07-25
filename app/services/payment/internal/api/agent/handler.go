// Package agent is the transport layer of the AgentService. Every RPC
// resolves the agent from the passport account — the payload never
// chooses whose inbox, wallets or assignments are touched.
package agent

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	transfermapper "mokapay.app/app/services/payment/internal/api/transfer/mapper"
	profileuc "mokapay.app/app/services/payment/internal/business/agentprofile/usecase"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	walletuc "mokapay.app/app/services/payment/internal/business/agentwallet/usecase"
	"mokapay.app/app/services/payment/internal/business/matching"
	matchinguc "mokapay.app/app/services/payment/internal/business/matching/usecase"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferuc "mokapay.app/app/services/payment/internal/business/transfer/usecase"
	"mokapay.app/app/services/payment/internal/sdk/grpcerr"
	"mokapay.app/app/services/payment/internal/sdk/identity"
	adminmapper "mokapay.app/app/services/payment/internal/api/admin/mapper"
	"mokapay.app/core/logger"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

type Handler struct {
	paymentv1.UnimplementedAgentServiceServer

	logger    *logger.Logger
	profiles  profileuc.UseCase
	matching  matchinguc.UseCase
	transfers transferuc.UseCase
	wallets   walletuc.UseCase
}

func NewHandler(l *logger.Logger, profiles profileuc.UseCase, matching matchinguc.UseCase, transfers transferuc.UseCase, wallets walletuc.UseCase) *Handler {
	return &Handler{
		logger:    l,
		profiles:  profiles,
		matching:  matching,
		transfers: transfers,
		wallets:   wallets,
	}
}

func (h *Handler) GetMyProfile(ctx context.Context, _ *paymentv1.GetMyProfileRequest) (*paymentv1.AgentProfile, error) {
	accountID, err := identity.RequireRoles(ctx, "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	profile, err := h.profiles.GetByAccount(ctx, accountID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return adminmapper.AgentProfileToProto(profile), nil
}

func (h *Handler) ListMyOffers(ctx context.Context, req *paymentv1.ListMyOffersRequest) (*paymentv1.ListMyOffersResponse, error) {
	accountID, err := identity.RequireRoles(ctx, "agent")
	if err != nil {
		return nil, err
	}

	var status *matching.OfferStatus
	if req.Status != nil {
		if s := transfermapper.OfferStatusFromProto(*req.Status); s != "" {
			status = new(s)
		}
	}

	offers, err := h.matching.ListOffers(ctx, accountID, status, req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.Offer, 0, len(offers))
	for _, o := range offers {
		out = append(out, transfermapper.OfferToProto(o))
	}

	return &paymentv1.ListMyOffersResponse{Offers: out}, nil
}

func (h *Handler) AcceptOffer(ctx context.Context, req *paymentv1.AcceptOfferRequest) (*paymentv1.Transfer, error) {
	accountID, err := identity.RequireRoles(ctx, "agent")
	if err != nil {
		return nil, err
	}

	offerID, err := uuid.Parse(req.OfferId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid offer id")
	}

	accepted, err := h.matching.AcceptOffer(ctx, accountID, offerID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return transfermapper.TransferToProto(transferuc.TransferView{Transfer: accepted}), nil
}

func (h *Handler) DeclineOffer(ctx context.Context, req *paymentv1.DeclineOfferRequest) (*paymentv1.Offer, error) {
	accountID, err := identity.RequireRoles(ctx, "agent")
	if err != nil {
		return nil, err
	}

	offerID, err := uuid.Parse(req.OfferId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid offer id")
	}

	declined, err := h.matching.DeclineOffer(ctx, accountID, offerID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	// The decline carries no transfer money summary — render the slot.
	return transfermapper.OfferToProto(matching.AgentOffer{Offer: declined}), nil
}

func (h *Handler) ListMyTransfers(ctx context.Context, req *paymentv1.ListMyTransfersRequest) (*paymentv1.ListTransfersResponse, error) {
	accountID, err := identity.RequireRoles(ctx, "agent")
	if err != nil {
		return nil, err
	}

	profile, err := h.profiles.GetByAccount(ctx, accountID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	list, err := h.transfers.ListForAgent(ctx, profile.ID, statusFilter(req.Status), req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.Transfer, 0, len(list))
	for _, t := range list {
		out = append(out, transfermapper.TransferToProto(transferuc.TransferView{Transfer: t}))
	}

	return &paymentv1.ListTransfersResponse{Transfers: out}, nil
}

func (h *Handler) ListMyWallets(ctx context.Context, _ *paymentv1.ListMyWalletsRequest) (*paymentv1.ListMyWalletsResponse, error) {
	accountID, err := identity.RequireRoles(ctx, "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	wallets, err := h.wallets.ListMine(ctx, accountID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.AgentWallet, 0, len(wallets))
	for _, w := range wallets {
		out = append(out, transfermapper.WalletToProto(w))
	}

	return &paymentv1.ListMyWalletsResponse{Wallets: out}, nil
}

func (h *Handler) AddWallet(ctx context.Context, req *paymentv1.AddWalletRequest) (*paymentv1.AgentWallet, error) {
	accountID, err := identity.RequireRoles(ctx, "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	op := transfermapper.OperatorFromProto(req.Operator)
	cur := transfermapper.CurrencyFromProto(req.Currency)
	if op == "" || cur == "" || req.Msisdn == "" {
		return nil, status.Error(codes.InvalidArgument, "operator, currency and msisdn are required")
	}

	w, err := h.wallets.Add(ctx, accountID, agentwallet.NewWallet{
		Operator: op,
		Currency: cur,
		Msisdn:   req.Msisdn,
	})
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return transfermapper.WalletToProto(w), nil
}

func (h *Handler) RemoveWallet(ctx context.Context, req *paymentv1.RemoveWalletRequest) (*emptypb.Empty, error) {
	accountID, err := identity.RequireRoles(ctx, "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid wallet id")
	}

	if err := h.wallets.Remove(ctx, accountID, id); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func statusFilter(s *paymentv1.TransferStatus) *transfer.Status {
	if s == nil {
		return nil
	}
	if st := transfermapper.StatusFromProto(*s); st != "" {
		return new(st)
	}
	return nil
}
