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
	adminmapper "mokapay.app/app/services/payment/internal/api/admin/mapper"
	transfermapper "mokapay.app/app/services/payment/internal/api/transfer/mapper"
	profileuc "mokapay.app/app/services/payment/internal/business/agentprofile/usecase"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	walletuc "mokapay.app/app/services/payment/internal/business/agentwallet/usecase"
	"mokapay.app/app/services/payment/internal/business/commission"
	commissionuc "mokapay.app/app/services/payment/internal/business/commission/usecase"
	"mokapay.app/app/services/payment/internal/business/ledger"
	"mokapay.app/app/services/payment/internal/business/matching"
	matchinguc "mokapay.app/app/services/payment/internal/business/matching/usecase"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferuc "mokapay.app/app/services/payment/internal/business/transfer/usecase"
	"mokapay.app/app/services/payment/internal/sdk/grpcerr"
	"mokapay.app/app/services/payment/internal/sdk/identity"
	"mokapay.app/core/logger"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

type Handler struct {
	paymentv1.UnimplementedAgentServiceServer

	logger      *logger.Logger
	profiles    profileuc.UseCase
	matching    matchinguc.UseCase
	transfers   transferuc.UseCase
	wallets     walletuc.UseCase
	commissions commissionuc.UseCase
	// The official receiving-account catalogue: startup configuration,
	// no usecase behind it.
	depositChannels []ledger.DepositChannel
}

func NewHandler(l *logger.Logger, profiles profileuc.UseCase, matching matchinguc.UseCase, transfers transferuc.UseCase, wallets walletuc.UseCase, commissions commissionuc.UseCase, depositChannels []ledger.DepositChannel) *Handler {
	return &Handler{
		logger:          l,
		profiles:        profiles,
		matching:        matching,
		transfers:       transfers,
		wallets:         wallets,
		commissions:     commissions,
		depositChannels: depositChannels,
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

// ListMyCommissions serves the agent's earnings — one accrual per
// completed transfer, with the status of the weekly payout carrying it.
func (h *Handler) ListMyCommissions(ctx context.Context, req *paymentv1.ListMyCommissionsRequest) (*paymentv1.ListMyCommissionsResponse, error) {
	accountID, err := identity.RequireRoles(ctx, "agent")
	if err != nil {
		return nil, err
	}

	var st *commission.Status
	if req.Status != nil {
		if s := adminmapper.CommissionStatusFromProto(*req.Status); s != "" {
			st = new(s)
		}
	}

	list, err := h.commissions.ListForAgent(ctx, accountID, st, req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.Commission, 0, len(list))
	for _, c := range list {
		out = append(out, adminmapper.CommissionToProto(c))
	}

	return &paymentv1.ListMyCommissionsResponse{Commissions: out}, nil
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
	if op == "" || req.Msisdn == "" {
		return nil, status.Error(codes.InvalidArgument, "operator and msisdn are required")
	}
	if !req.AcceptsUsd && !req.AcceptsCdf {
		return nil, status.Error(codes.InvalidArgument, "the wallet must accept at least one currency")
	}

	w, err := h.wallets.Add(ctx, accountID, agentwallet.NewWallet{
		Operator:   op,
		Msisdn:     req.Msisdn,
		AcceptsUSD: req.AcceptsUsd,
		AcceptsCDF: req.AcceptsCdf,
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

// ListDepositChannels serves the official receiving accounts for funding
// deposits. pending_agent included: the guarantee (G2) is funded during
// enrollment, before the role flips to agent.
func (h *Handler) ListDepositChannels(ctx context.Context, _ *paymentv1.ListDepositChannelsRequest) (*paymentv1.ListDepositChannelsResponse, error) {
	if _, err := identity.RequireRoles(ctx, "agent", "pending_agent"); err != nil {
		return nil, err
	}

	out := make([]*paymentv1.DepositChannel, 0, len(h.depositChannels))
	for _, c := range h.depositChannels {
		out = append(out, &paymentv1.DepositChannel{
			Name:          c.Name,
			Kind:          c.Kind,
			AccountName:   c.AccountName,
			AccountNumber: c.AccountNumber,
			Instructions:  c.Instructions,
		})
	}

	return &paymentv1.ListDepositChannelsResponse{Channels: out}, nil
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
