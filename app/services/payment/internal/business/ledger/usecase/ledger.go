// Package usecase implements the ledger request engine: creation with
// role-derived landing status and direction, forward/backward moves
// between the open states, and the closing decisions (approve = settle,
// reject = release). Money movement itself lives in the payments_ext
// procedures — this layer owns legality, identity and notifications.
package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	"mokapay.app/app/services/payment/internal/business/ledger/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
)

// Notifier schedule river job to send notification to user if needed.
type Notifier interface {
	Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error
}

// mapStatusTransition - contains all the valid transitions between OPEN
// states (UpdateStatus territory). Closing is never here: done only via
// Approve (settlement), rejected only via Reject (release).
var mapStatusTransition = map[ledger.Status]map[ledger.Status]bool{
	ledger.StatusInitiated: {
		ledger.StatusPendingAdminApproval: true,
		ledger.StatusPendingAgentAction:   true,
	},
	ledger.StatusPendingAgentAction: {
		ledger.StatusPendingAdminApproval: true,
	},
	// The admin can send a request back for a better proof.
	ledger.StatusPendingAdminApproval: {
		ledger.StatusPendingAgentAction: true,
	},
}

// The caller never decides the direction a ledger has — it follows the
// type. Deduction/commission arrive with their volleys (capture, payout).
var mapTypeToDirection = map[ledger.Type]ledger.Direction{
	ledger.TypeWithdrawal: ledger.DirectionOutflow,
	ledger.TypeRefund:     ledger.DirectionOutflow,
	ledger.TypeFunding:    ledger.DirectionInflow,
}

type UseCase struct {
	logger   *logger.Logger
	repo     repo.Store
	profiles profilerepo.Store
	notifier Notifier
	db       *coredb.Postgres
}

func New(l *logger.Logger, db *coredb.Postgres, n Notifier) UseCase {
	return UseCase{
		logger:   l,
		repo:     repo.New(db),
		profiles: profilerepo.New(db),
		notifier: n,
		db:       db,
	}
}

// CreateLedgerRequest - create a new ledger.LedgerRequest
// Currently in the system a ledger request can be open by
// Admin/ops - type: funding ( for an enrollment first time funding ), refund ( for transaction refunds )
// Agent - funding ( for general balance funding ), Withdrawal ( to remove x amount from his balance )
func (u UseCase) CreateLedgerRequest(ctx context.Context, pass *passport.Passport, nr ledger.NewRequest) (ledger.LedgerRequest, error) {
	if pass == nil {
		return ledger.LedgerRequest{}, ledger.ErrNotAllowed
	}

	direction, ok := mapTypeToDirection[nr.Type]
	if !ok {
		return ledger.LedgerRequest{}, ledger.ErrInvalidType
	}

	// The creator is always the passport identity, never wire input —
	// passed explicitly down to the repo, never silently populated.
	caller, err := uuid.Parse(pass.User.ID)
	if err != nil {
		return ledger.LedgerRequest{}, ledger.ErrNotAllowed
	}

	var status ledger.Status
	switch pass.User.Role {
	case "agent":
		if nr.Type != ledger.TypeWithdrawal && nr.Type != ledger.TypeFunding {
			return ledger.LedgerRequest{}, ledger.ErrInvalidType
		}
		// An agent only ever moves their OWN money: the profile comes
		// from the passport, whatever the payload claims.
		profile, err := u.profiles.GetByAccount(ctx, pass.User.ID)
		if err != nil {
			return ledger.LedgerRequest{}, err
		}
		nr.AgentProfileID = profile.ID
		status = ledger.StatusPendingAdminApproval
	case "admin", "ops":
		if nr.Type != ledger.TypeFunding && nr.Type != ledger.TypeRefund {
			return ledger.LedgerRequest{}, ledger.ErrInvalidType
		}
		if nr.Type == ledger.TypeFunding {
			if nr.Reference == nil || (nr.Reference.ID == uuid.Nil || nr.Reference.Type != ledger.ReferenceEnrollment) {
				return ledger.LedgerRequest{}, ledger.ErrInvalidType
			}
		}
		status = ledger.StatusPendingAgentAction
	default:
		return ledger.LedgerRequest{}, ledger.ErrNotAllowed
	}

	created, err := u.repo.Add(ctx, status, direction, caller, nr)
	if err != nil {
		if !errors.Is(err, ledger.ErrNotFeasible) && !errors.Is(err, ledger.ErrReferenceInUse) {
			u.logger.Error(ctx, "Ledger.CreateLedgerRequest: failed", "err", err)
		}
		return ledger.LedgerRequest{}, err
	}

	return created, nil
}

func (u UseCase) GetLedgerRequest(ctx context.Context, id uuid.UUID) (ledger.LedgerRequest, error) {
	return u.repo.Get(ctx, id.String())
}

func (u UseCase) GetLedgerRequestByReference(ctx context.Context, ref ledger.Reference) (ledger.LedgerRequest, error) {
	return u.repo.GetByReference(ctx, ref)
}

func (u UseCase) ListLedgerRequests(ctx context.Context, f ledger.Filter) ([]ledger.LedgerRequest, error) {
	return u.repo.List(ctx, f)
}

// ListLedgerLines returns a profile's settlement statement, newest first.
func (u UseCase) ListLedgerLines(ctx context.Context, agentProfileID uuid.UUID, limit int32) ([]ledger.Line, error) {
	if limit <= 0 {
		limit = 50
	}
	return u.repo.ListLines(ctx, agentProfileID.String(), limit)
}

// UpdateLedgerRequestStatus - move ledger.LedgerRequest status
// Allow the status to move only to non closing states ( so not done, rejected )
func (u UseCase) UpdateLedgerRequestStatus(ctx context.Context, pass *passport.Passport, id uuid.UUID, newStatus ledger.Status, proofID *uuid.UUID) error {
	// currently restrict this endpoint to system call only
	if pass == nil || !pass.IsSystem() {
		return ledger.ErrNotAllowed
	}

	l, err := u.repo.Get(ctx, id.String())
	if err != nil {
		return err
	}

	if l.Status == ledger.StatusRejected || l.Status == ledger.StatusDone {
		return ledger.ErrLedgerStatusClose
	}

	if currentStatusMap := mapStatusTransition[l.Status]; len(currentStatusMap) == 0 || !currentStatusMap[newStatus] {
		return ledger.ErrInvalidStatusMove
	}

	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		// The status move and the notification enqueue commit together.
		if _, err := u.repo.WithTx(tx).UpdateStatus(ctx, id.String(), newStatus, proofID); err != nil {
			return err
		}

		switch newStatus {
		case ledger.StatusPendingAdminApproval:
			// The ball is with the admin who opened the request (or who
			// will pick it from the worklist) — the creator is the best
			// single recipient we have.
			return u.notifier.Notify(
				ctx,
				tx,
				l.CreatedBy.String(),
				"ledger.proof_received",
				"Votre validation est requise",
				"L'agent a téléversé une preuve de paiement.",
			)
		case ledger.StatusPendingAgentAction:
			// The agent acts next — notify the profile owner, NOT the
			// creator (on admin-created requests those differ).
			profile, err := u.profiles.Get(ctx, l.AgentProfileID.String())
			if err != nil {
				return err
			}
			return u.notifier.Notify(
				ctx,
				tx,
				profile.AccountID.String(),
				"ledger.proof_needed",
				"Preuve de paiement requise",
				"Une preuve de paiement est attendue de votre part.",
			)
		}

		return nil
	}); err != nil {
		u.logger.Error(ctx, "Ledger.UpdateLedgerRequestStatus: failed to update ledger status", "err", err)
		return fmt.Errorf("failed to update ledger status: %w", err)
	}

	return nil
}

// ApproveLedgerRequest settles: request → done, balance moved, one
// immutable line — one statement in the store. Idempotent for retrying
// callers: approving an already-done request returns it unchanged.
func (u UseCase) ApproveLedgerRequest(ctx context.Context, decidedBy, id uuid.UUID) (ledger.LedgerRequest, error) {
	approved, err := u.repo.Approve(ctx, id.String(), decidedBy.String())
	if err == nil {
		return approved, nil
	}
	if !errors.Is(err, ledger.ErrRequestNotFound) {
		u.logger.Error(ctx, "Ledger.ApproveLedgerRequest: failed", "err", err)
		return ledger.LedgerRequest{}, err
	}

	// Zero rows: unknown id, or not pending approval. Read back to tell —
	// done means a retried approval (fine), anything else is a real error.
	current, gerr := u.repo.Get(ctx, id.String())
	if gerr != nil {
		return ledger.LedgerRequest{}, gerr
	}
	if current.Status == ledger.StatusDone {
		return current, nil
	}
	return ledger.LedgerRequest{}, ledger.ErrInvalidStatusMove
}

// RejectLedgerRequest closes the request from any open state, releasing
// an outflow's reservation in the same statement.
func (u UseCase) RejectLedgerRequest(ctx context.Context, decidedBy, id uuid.UUID, reason string) (ledger.LedgerRequest, error) {
	var r *string
	if reason != "" {
		r = new(reason)
	}

	rejected, err := u.repo.Reject(ctx, id.String(), decidedBy.String(), r)
	if err != nil {
		if errors.Is(err, ledger.ErrRequestNotFound) {
			// Unknown id or already closed — read back to say which.
			if current, gerr := u.repo.Get(ctx, id.String()); gerr == nil {
				_ = current // exists but closed
				return ledger.LedgerRequest{}, ledger.ErrLedgerStatusClose
			}
			return ledger.LedgerRequest{}, ledger.ErrRequestNotFound
		}
		u.logger.Error(ctx, "Ledger.RejectLedgerRequest: failed", "err", err)
		return ledger.LedgerRequest{}, err
	}

	return rejected, nil
}
