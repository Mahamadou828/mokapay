// Package usecase implements the agent-enrollment workflow. Every
// transition that changes an account role runs the enrollment procedure
// and the role flip on one transaction — they commit or roll back
// together. Notifications ride outside the transaction (best-effort, the
// notification usecase owns its persistence).
package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/business/auth"
	authrepo "mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/business/enrollment/repo"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/core/client"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
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
	moka       *client.MokaClient
	tosVersion string
}

func New(l *logger.Logger, db *coredb.Postgres, notifier Notifier, moka *client.MokaClient, tosVersion string) UseCase {
	return UseCase{
		logger:     l,
		db:         db,
		repo:       repo.New(db),
		accounts:   authrepo.New(db),
		notifier:   notifier,
		moka:       moka,
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

	// The money side exists BEFORE the terms stamp, so if the accept
	// below fails the candidate simply retries — nothing dangles. Profile
	// creation runs under the system passport (creation is plumbing, not
	// a decision) and is idempotent (AlreadyExists = resolve the existing
	// one).
	profile, err := u.ensureAgentProfile(ctx, accountID)
	if err != nil {
		u.logger.Error(ctx, "Enrollment.Accept: failed to ensure agent profile", "err", err)
		return enrollment.Enrollment{}, fmt.Errorf("creating agent profile: %w", err)
	}

	// The guarantee funding request opens on the same accept — no admin
	// click in between. It rides the CANDIDATE's passport: they are the
	// accountable creator (they send the money and attach the proof),
	// and it references this enrollment so Approve can find and settle
	// it. AlreadyExists = an earlier accept attempt got here — success.
	if profile.GuaranteeFloor > 0 {
		if _, err := u.moka.Payment.Ledger.CreateLedgerRequest(ctx, &paymentv1.CreateLedgerRequestRequest{
			Type:                paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_FUNDING,
			Amount:              profile.GuaranteeFloor,
			ReferenceEntityType: new(paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_ENROLLMENT),
			ReferenceEntityId:   new(id.String()),
		}); err != nil && status.Code(err) != codes.AlreadyExists {
			u.logger.Error(ctx, "Enrollment.Accept: failed to open guarantee funding request", "err", err)
			return enrollment.Enrollment{}, fmt.Errorf("opening guarantee funding request: %w", err)
		}
	} else {
		// A zero floor means no guarantee is owed; Approve will still
		// require an admin-created request, so surface it in the logs.
		u.logger.Info(ctx, "Enrollment.Accept: guarantee floor is zero, no funding request opened", "account", accountID)
	}

	accepted, err := u.repo.Accept(ctx, id.String(), accountID.String(), tosVersion)
	if err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
			u.logger.Error(ctx, "Enrollment.Accept: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

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
	current, err := u.repo.Get(ctx, id.String())
	if err != nil {
		return enrollment.Enrollment{}, err
	}
	// A retried approval after a partial failure: the payment side is
	// already settled, the local transition already committed.
	if current.Status == enrollment.StatusApproved {
		return current, nil
	}

	// Remote side FIRST, outside any transaction (a remote call must
	// never hold row locks open). Both calls are idempotent: if the local
	// tx below fails, the admin just retries. They run under the CALLER's
	// passport on purpose — settlement needs an accountable human, and
	// the approving admin becomes the ledger's decided_by.
	if err := u.settleGuarantee(ctx, id); err != nil {
		if !errors.Is(err, enrollment.ErrGuaranteeNotSettled) {
			u.logger.Error(ctx, "Enrollment.Approve: failed to settle guarantee", "err", err)
		}
		return enrollment.Enrollment{}, err
	}
	if err := u.activateAgentProfile(ctx, current.AccountID); err != nil {
		u.logger.Error(ctx, "Enrollment.Approve: failed to activate agent profile", "err", err)
		return enrollment.Enrollment{}, err
	}

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

// ensureAgentProfile creates the candidate's money account, or resolves
// the existing one — Accept needs the profile either way (its guarantee
// floor sizes the funding request).
func (u UseCase) ensureAgentProfile(ctx context.Context, accountID uuid.UUID) (*paymentv1.AgentProfile, error) {
	sysCtx := passport.AddToContext(ctx, passport.SystemPassport())

	created, err := u.moka.Payment.Admin.CreateAgentProfile(sysCtx, &paymentv1.CreateAgentProfileRequest{
		AccountId: accountID.String(),
	})
	if err == nil {
		return created, nil
	}
	if status.Code(err) != codes.AlreadyExists {
		return nil, err
	}

	return u.moka.Payment.Admin.GetAgentProfile(sysCtx, &paymentv1.GetAgentProfileRequest{
		Selector: &paymentv1.GetAgentProfileRequest_AccountId{AccountId: accountID.String()},
	})
}

// settleGuarantee approves the enrollment's guarantee funding request on
// the payment side; enrollment.ErrGuaranteeNotSettled when it is missing
// or not yet approvable (no proof uploaded, sent back, rejected…).
func (u UseCase) settleGuarantee(ctx context.Context, enrollmentID uuid.UUID) error {
	req, err := u.moka.Payment.Ledger.GetLedgerRequestByReference(ctx, &paymentv1.GetLedgerRequestByReferenceRequest{
		ReferenceEntityType: paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_ENROLLMENT,
		ReferenceEntityId:   enrollmentID.String(),
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return enrollment.ErrGuaranteeNotSettled
		}
		return fmt.Errorf("resolving guarantee request: %w", err)
	}

	if _, err := u.moka.Payment.Ledger.ApproveLedgerRequest(ctx, &paymentv1.ApproveLedgerRequestRequest{
		Id: req.Id,
	}); err != nil {
		// The payment engine treats an already-done request as success,
		// so a retry never errors on the done case.
		if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.NotFound {
			return enrollment.ErrGuaranteeNotSettled
		}
		return fmt.Errorf("approving guarantee request: %w", err)
	}

	return nil
}

// activateAgentProfile flips the money account live (idempotent
// payment-side, so approve retries pass).
func (u UseCase) activateAgentProfile(ctx context.Context, accountID uuid.UUID) error {
	profile, err := u.moka.Payment.Admin.GetAgentProfile(ctx, &paymentv1.GetAgentProfileRequest{
		Selector: &paymentv1.GetAgentProfileRequest_AccountId{AccountId: accountID.String()},
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return enrollment.ErrGuaranteeNotSettled
		}
		return fmt.Errorf("resolving agent profile: %w", err)
	}

	if _, err := u.moka.Payment.Admin.ActivateAgentProfile(ctx, &paymentv1.ActivateAgentProfileRequest{
		Id: profile.Id,
	}); err != nil {
		return fmt.Errorf("activating agent profile: %w", err)
	}
	return nil
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
