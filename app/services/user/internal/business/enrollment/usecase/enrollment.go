// Package usecase implements the agent-enrollment workflow. Every
// transition that changes an account role runs the enrollment procedure
// and the role flip on one transaction — they commit or roll back
// together. Notifications ride outside the transaction (best-effort, the
// notification usecase owns its persistence).
package usecase

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"mokapay.app/app/services/user/internal/business/auth"
	authrepo "mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/business/enrollment/repo"
	"mokapay.app/app/services/user/internal/business/notification"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Notifier announces enrollment transitions to the candidate (satisfied by
// the notification usecase — same service, no gRPC hop). Push failures are
// logged, never propagated: the transition already committed.
type Notifier interface {
	Push(ctx context.Context, nn notification.NewNotification) (notification.Notification, error)
}

type UseCase struct {
	logger     *logger.Logger
	db         *coredb.Postgres
	repo       repo.Store
	accounts   authrepo.Store
	notifier   Notifier
	tosVersion string
}

func New(l *logger.Logger, db *coredb.Postgres, notifier Notifier, tosVersion string) UseCase {
	return UseCase{
		logger:     l,
		db:         db,
		repo:       repo.New(db),
		accounts:   authrepo.New(db),
		notifier:   notifier,
		tosVersion: tosVersion,
	}
}

// Invite creates the enrollment and flips the candidate to pending_agent
// atomically, then notifies them.
func (u UseCase) Invite(ctx context.Context, accountID, invitedBy uuid.UUID) (enrollment.Enrollment, error) {
	acc, err := u.accounts.GetAccount(ctx, accountID.String())
	if err != nil {
		return enrollment.Enrollment{}, err
	}
	if acc.Status != auth.AccountStatusActive || acc.Role != auth.AccountRoleUser {
		return enrollment.Enrollment{}, enrollment.ErrNotEligible
	}

	var created enrollment.Enrollment
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		created, err = u.repo.WithTx(tx).Add(ctx, accountID.String(), invitedBy.String())
		if err != nil {
			return err
		}
		return u.setRole(ctx, tx, accountID, auth.AccountRolePendingAgent)
	}); err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentPending) {
			u.logger.Error(ctx, "Enrollment.Invite: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	u.notify(ctx, accountID, "enrollment.invited",
		"Become a MOKA PAY agent",
		"You have been invited to become an agent. Review the terms to get started.")

	return created, nil
}

// MyEnrollment returns the caller's latest request, whatever its status.
func (u UseCase) MyEnrollment(ctx context.Context, accountID uuid.UUID) (enrollment.Enrollment, error) {
	return u.repo.GetLatestByAccount(ctx, accountID.String())
}

// List returns enrollments matching the filter (ops dashboard).
func (u UseCase) List(ctx context.Context, f enrollment.Filter) ([]enrollment.Enrollment, error) {
	return u.repo.List(ctx, f)
}

// Accept stamps the candidate's TOS acceptance. The role does not change
// here — that is the admin approval's job.
func (u UseCase) Accept(ctx context.Context, accountID, id uuid.UUID, tosVersion string) (enrollment.Enrollment, error) {
	if tosVersion != u.tosVersion {
		return enrollment.Enrollment{}, enrollment.ErrTOSVersionMismatch
	}

	accepted, err := u.repo.Accept(ctx, id.String(), accountID.String(), tosVersion)
	if err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
			u.logger.Error(ctx, "Enrollment.Accept: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	// TODO(Mahamadou): payment.AgentService.CreateProfile — create the
	// (inactive) agent profile the guarantee deposit will target.
	// TODO(Mahamadou): payment.LedgerService.CreateRequest — the 20 USD
	// guarantee FUNDING ledger_request, carrying this enrollment's id as
	// enrollment_nk. Its settlement is what the approving admin checks.

	return accepted, nil
}

// Decline (candidate, only from invited) restores the user role
// atomically with the status flip.
func (u UseCase) Decline(ctx context.Context, accountID, id uuid.UUID) (enrollment.Enrollment, error) {
	var declined enrollment.Enrollment
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var err error
		declined, err = u.repo.WithTx(tx).Decline(ctx, id.String(), accountID.String())
		if err != nil {
			return err
		}
		return u.setRole(ctx, tx, accountID, auth.AccountRoleUser)
	}); err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
			u.logger.Error(ctx, "Enrollment.Decline: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	return declined, nil
}

// Cancel (admin, from either live state) restores the candidate's user
// role atomically with the status flip.
func (u UseCase) Cancel(ctx context.Context, decidedBy, id uuid.UUID) (enrollment.Enrollment, error) {
	var cancelled enrollment.Enrollment
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var err error
		cancelled, err = u.repo.WithTx(tx).Cancel(ctx, id.String(), decidedBy.String())
		if err != nil {
			return err
		}
		return u.setRole(ctx, tx, cancelled.AccountID, auth.AccountRoleUser)
	}); err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
			u.logger.Error(ctx, "Enrollment.Cancel: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	return cancelled, nil
}

// Approve (admin, only from accepted) promotes the candidate to agent —
// after re-checking the account is still active (moderation can strike
// mid-flow) — then notifies them.
func (u UseCase) Approve(ctx context.Context, decidedBy, id uuid.UUID) (enrollment.Enrollment, error) {
	var approved enrollment.Enrollment
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var err error
		approved, err = u.repo.WithTx(tx).Approve(ctx, id.String(), decidedBy.String())
		if err != nil {
			return err
		}

		acc, err := u.accounts.WithTx(tx).GetAccount(ctx, approved.AccountID.String())
		if err != nil {
			return err
		}
		if acc.Status != auth.AccountStatusActive {
			return enrollment.ErrNotEligible // rolls the approval back
		}

		return u.setRole(ctx, tx, approved.AccountID, auth.AccountRoleAgent)
	}); err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentNotFound) && !errors.Is(err, enrollment.ErrNotEligible) {
			u.logger.Error(ctx, "Enrollment.Approve: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	u.notify(ctx, approved.AccountID, "enrollment.approved",
		"You are now a MOKA PAY agent",
		"Your enrollment has been approved. Welcome aboard!")

	return approved, nil
}

func (u UseCase) setRole(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, role auth.AccountRole) error {
	_, err := u.accounts.WithTx(tx).UpdateAccount(ctx, auth.UpdateAccount{
		ID:   accountID,
		Role: new(role),
	})
	return err
}

func (u UseCase) notify(ctx context.Context, accountID uuid.UUID, eventType, title, body string) {
	if _, err := u.notifier.Push(ctx, notification.NewNotification{
		AccountID: accountID,
		EventType: eventType,
		Title:     title,
		Body:      body,
	}); err != nil {
		// The transition already committed — a lost notification is a log
		// line, not a failure.
		u.logger.Error(ctx, "Enrollment: failed to notify", "err", err, "event", eventType, "account", accountID)
	}
}
