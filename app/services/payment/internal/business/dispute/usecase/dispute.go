// Package usecase owns the dispute flow: who may freeze a transfer, the
// system's auto-dispute when an agent goes silent on a funded transfer,
// and the admin ruling that ends it. The money effects are applied by the
// payments_ext procedures; this layer owns legality, identity, the
// commission that a favourable ruling still earns, and the D10 shortfall
// alert.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/commission"
	commissionrepo "mokapay.app/app/services/payment/internal/business/commission/repo"
	"mokapay.app/app/services/payment/internal/business/dispute"
	"mokapay.app/app/services/payment/internal/business/dispute/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Jobs is the River publisher the usecase enqueues through — always in
// the SAME transaction as the ruling that causes it, so the parties learn
// of a freeze or a ruling with the commit behind it, and a transfer a
// ruling completed is evaluated exactly like one the client confirmed.
// Implemented by the transfer events package.
type Jobs interface {
	Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error
	ScheduleComplianceEvaluation(ctx context.Context, tx pgx.Tx, transferID uuid.UUID) error
}

type UseCase struct {
	logger      *logger.Logger
	db          *coredb.Postgres
	disputes    repo.Store
	transfers   transferrepo.Store
	commissions commissionrepo.Store
	profiles    profilerepo.Store
	jobs        Jobs
}

func New(l *logger.Logger, db *coredb.Postgres, j Jobs) UseCase {
	return UseCase{
		logger:      l,
		db:          db,
		disputes:    repo.New(db),
		transfers:   transferrepo.New(db),
		commissions: commissionrepo.New(db),
		profiles:    profilerepo.New(db),
		jobs:        j,
	}
}

// Open freezes a transfer on behalf of one of its parties. The party is
// derived from the passport identity against the transfer itself — a
// caller with no standing gets the same answer as for a missing transfer.
func (u UseCase) Open(ctx context.Context, callerID, transferID uuid.UUID, reason dispute.Reason, details *string) (dispute.Dispute, error) {
	t, err := u.transfers.Get(ctx, transferID.String())
	if err != nil {
		return dispute.Dispute{}, err
	}

	party, err := u.partyOf(ctx, callerID, t)
	if err != nil {
		return dispute.Dispute{}, err
	}

	return u.open(ctx, t, dispute.NewDispute{
		TransferID: t.ID,
		Party:      party,
		OpenedBy:   new(callerID),
		Reason:     reason,
		Details:    details,
	})
}

// HandlePayoutTimeout is the auto-dispute (spec: "system auto-dispute on
// timeout"): the client paid, the agent never reported the payout within
// the window. The transfer freezes with its hold intact and lands on the
// ops queue. A transfer that moved on is a no-op — the clock is stale.
func (u UseCase) HandlePayoutTimeout(ctx context.Context, transferID uuid.UUID) error {
	t, err := u.transfers.Get(ctx, transferID.String())
	if err != nil {
		if errors.Is(err, transfer.ErrTransferNotFound) {
			return nil
		}
		return err
	}
	if t.Status != transfer.StatusPendingAgentTransfer {
		return nil // the payout proof arrived (or ops stepped in) meanwhile
	}

	details := "agent did not report the payout within the allowed window"
	if _, err := u.open(ctx, t, dispute.NewDispute{
		TransferID: t.ID,
		Party:      dispute.PartySystem,
		Reason:     dispute.ReasonAgentNotPaid,
		Details:    new(details),
	}); err != nil {
		if errors.Is(err, dispute.ErrNotDisputable) {
			return nil // raced with the proof — nothing to do
		}
		u.logger.Error(ctx, "Dispute.HandlePayoutTimeout: failed", "err", err)
		return err
	}

	return nil
}

// Get returns one dispute.
func (u UseCase) Get(ctx context.Context, id uuid.UUID) (dispute.Dispute, error) {
	return u.disputes.Get(ctx, id.String())
}

// List is the ops queue (optional filters).
func (u UseCase) List(ctx context.Context, f dispute.Filter, limit int32) ([]dispute.Dispute, error) {
	if limit <= 0 {
		limit = 50
	}
	return u.disputes.List(ctx, f, limit)
}

// Resolve applies the admin's ruling. The resolution table (dispute
// domain) decides everything: release or capture, where the transfer
// lands, whether the client is refunded — and whether the agent still
// earned their commission. All of it commits together.
func (u UseCase) Resolve(ctx context.Context, adminID, id uuid.UUID, resolution dispute.Resolution, note *string) (dispute.Dispute, error) {
	effect, err := dispute.EffectOf(resolution)
	if err != nil {
		return dispute.Dispute{}, err
	}

	var resolved dispute.Dispute
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		disputes := u.disputes.WithTx(tx)

		var rerr error
		if effect.HoldOutcome == dispute.HoldCaptured {
			resolved, rerr = disputes.ResolveCapture(ctx, id.String(), adminID, note)
		} else {
			resolved, rerr = disputes.ResolveRelease(ctx, id.String(), resolution, effect.TerminalStatus, adminID, note)
		}
		if rerr != nil {
			return rerr
		}

		t, terr := u.transfers.WithTx(tx).Get(ctx, resolved.TransferID.String())
		if terr != nil {
			return terr
		}

		// A ruling that leaves the payout standing completes the transfer,
		// and a completion books the same two things here as on the happy
		// path: the agent's share (the same accrual, through the same
		// idempotent constraint) and the AML evaluation of the movement.
		// A disputed transfer that still completed is more interesting to
		// compliance than a quiet one, not less.
		if effect.Completes() {
			if err := u.accrue(ctx, tx, t); err != nil {
				return err
			}
			if err := u.jobs.ScheduleComplianceEvaluation(ctx, tx, t.ID); err != nil {
				return err
			}
		}

		return u.notifyResolution(ctx, tx, t, effect, resolution)
	}); err != nil {
		if !errors.Is(err, dispute.ErrDisputeNotFound) {
			u.logger.Error(ctx, "Dispute.Resolve: failed", "err", err)
		}
		return dispute.Dispute{}, err
	}

	u.warnRefundShortfall(ctx, resolved, effect)

	return resolved, nil
}

// open freezes the transfer, tells both sides and commits the three
// together.
func (u UseCase) open(ctx context.Context, t transfer.Transfer, nd dispute.NewDispute) (dispute.Dispute, error) {
	var opened dispute.Dispute
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var oerr error
		opened, oerr = u.disputes.WithTx(tx).Open(ctx, nd)
		if oerr != nil {
			return oerr
		}

		const (
			title = "Transfert en litige"
			event = "transfer.disputed"
		)
		msg := fmt.Sprintf("Le transfert %s est en litige — notre équipe examine le dossier.", t.Reference)
		if err := u.jobs.Notify(ctx, tx, t.ClientID.String(), event, title, msg); err != nil {
			return err
		}
		return u.notifyAgent(ctx, tx, t, event, title, msg)
	}); err != nil {
		if !errors.Is(err, dispute.ErrNotDisputable) {
			u.logger.Error(ctx, "Dispute.Open: failed", "err", err)
		}
		return dispute.Dispute{}, err
	}

	return opened, nil
}

// accrue writes the transfer's commission inside the caller's
// transaction. An already-accrued transfer is success, not an error: the
// UNIQUE on transfer_fk is the whole double-booking guard.
func (u UseCase) accrue(ctx context.Context, tx pgx.Tx, t transfer.Transfer) error {
	nc, err := commission.NewAccrual(t, time.Now())
	if err != nil {
		return err
	}
	if _, err := u.commissions.WithTx(tx).Accrue(ctx, nc); err != nil &&
		!errors.Is(err, commission.ErrCommissionExists) {
		return err
	}

	return nil
}

// partyOf places the caller in the transfer: its owner disputes as the
// client, the assigned agent as the agent. Anyone else has no standing —
// reported as a missing transfer, never as "not yours".
func (u UseCase) partyOf(ctx context.Context, callerID uuid.UUID, t transfer.Transfer) (dispute.Party, error) {
	if t.ClientID == callerID {
		return dispute.PartyClient, nil
	}
	if t.AgentProfileID != nil {
		profile, err := u.profiles.Get(ctx, t.AgentProfileID.String())
		if err == nil && profile.AccountID == callerID {
			return dispute.PartyAgent, nil
		}
	}

	return "", transfer.ErrTransferNotFound
}

func (u UseCase) notifyResolution(ctx context.Context, tx pgx.Tx, t transfer.Transfer, effect dispute.Effect, resolution dispute.Resolution) error {
	const (
		title = "Litige résolu"
		event = "transfer.dispute_resolved"
	)

	clientMsg := fmt.Sprintf("Le litige sur le transfert %s est résolu.", t.Reference)
	if effect.RefundClient {
		clientMsg = fmt.Sprintf("Le litige sur le transfert %s est résolu en votre faveur — vous serez remboursé.", t.Reference)
	}
	if err := u.jobs.Notify(ctx, tx, t.ClientID.String(), event, title, clientMsg); err != nil {
		return err
	}

	agentMsg := fmt.Sprintf("Le litige sur le transfert %s est résolu — votre garantie est libérée.", t.Reference)
	if resolution == dispute.ResolutionAgentFault {
		agentMsg = fmt.Sprintf("Le litige sur le transfert %s est résolu à votre charge — votre garantie a été prélevée.", t.Reference)
	}

	return u.notifyAgent(ctx, tx, t, event, title, agentMsg)
}

func (u UseCase) notifyAgent(ctx context.Context, tx pgx.Tx, t transfer.Transfer, event, title, message string) error {
	if t.AgentProfileID == nil {
		return nil
	}
	agent, err := u.profiles.Get(ctx, t.AgentProfileID.String())
	if err != nil {
		return err
	}

	return u.jobs.Notify(ctx, tx, agent.AccountID.String(), event, title, message)
}

// warnRefundShortfall is D10: the client's refund is backed by the
// captured guarantee, and if the capture does not cover it the gap comes
// out of the platform reserve. The hold was stamped as the client's FULL
// total (converted at the transfer's own locked rate for CDF sources), so
// this is zero by construction — it is checked rather than assumed,
// because the number would be real money the platform owes. v1 logs and
// leaves it to ops; the `platform_reserve` bucket is named, not built.
func (u UseCase) warnRefundShortfall(ctx context.Context, d dispute.Dispute, effect dispute.Effect) {
	// Only a capture funds a refund, and only a same-denomination
	// comparison is meaningful: a CDF total was converted INTO the hold, so
	// the hold covers it by construction.
	if !effect.RefundClient || d.RefundCurrency != transfer.CurrencyUSD {
		return
	}
	if d.HoldAmount >= d.RefundAmount {
		return
	}

	u.logger.Error(ctx, "Dispute.Resolve: refund shortfall — platform_reserve owes the difference (D10)",
		"dispute_id", d.ID,
		"transfer_id", d.TransferID,
		"captured", d.HoldAmount,
		"refund_owed", d.RefundAmount,
		"shortfall", d.RefundAmount-d.HoldAmount)
}
