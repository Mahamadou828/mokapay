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

// SelfEnroll is the self-serve entry point (AG-04): no invite, no
// inviter, and the terms are stamped by this very call rather than by a
// later Accept. It lands the candidate exactly where an invited candidate
// lands after accepting — status accepted, role pending_agent, agent
// profile provisioned, guarantee funding open — so the funding screens and
// the ops deposit worklist work identically for both. What differs is only
// the exit: SettleGuarantee (driven by ops approving the deposit) rather
// than an admin's Approve.
//
// Retrying is safe. A live self-serve request is resumed rather than
// rejected, because the money-side work that follows creation can fail on
// its own and the candidate must be able to press the button again.
func (u UseCase) SelfEnroll(ctx context.Context, accountID uuid.UUID, in enrollment.SelfEnrollment) (enrollment.Enrollment, error) {
	if in.TOSVersion != u.tosVersion {
		return enrollment.Enrollment{}, enrollment.ErrTOSVersionMismatch
	}

	acc, err := u.accounts.GetAccount(ctx, accountID.String())
	if err != nil {
		return enrollment.Enrollment{}, err
	}
	// pending_agent is admitted for the retry case only — the row check
	// below is what actually decides whether there is something to resume.
	if acc.Status != auth.AccountStatusActive ||
		(acc.Role != auth.AccountRoleUser && acc.Role != auth.AccountRolePendingAgent) {
		return enrollment.Enrollment{}, enrollment.ErrNotEligible
	}

	created, fresh, err := u.createOrResumeSelfEnrollment(ctx, accountID, in.TOSVersion)
	if err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentPending) {
			u.logger.Error(ctx, "Enrollment.SelfEnroll: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	// The money side, in the same order and with the same idempotency the
	// invited path's Accept uses. It runs after the row exists because the
	// funding request references this enrollment by id.
	if err := u.openGuarantee(ctx, accountID, created.ID, in.City); err != nil {
		u.logger.Error(ctx, "Enrollment.SelfEnroll: failed to open guarantee", "err", err)
		return enrollment.Enrollment{}, err
	}

	if fresh {
		u.notify(ctx, accountID, "enrollment.self_enrolled",
			"Your agent application is in",
			"Fund your guarantee deposit to activate your agent account.")
	}

	return created, nil
}

// createOrResumeSelfEnrollment writes the request and flips the role on one
// transaction. The bool reports whether the row is new: a resumed one must
// not re-notify. A live INVITED request wins — the candidate has to answer
// the invitation they already have rather than open a second track.
func (u UseCase) createOrResumeSelfEnrollment(ctx context.Context, accountID uuid.UUID, tosVersion string) (enrollment.Enrollment, bool, error) {
	var created enrollment.Enrollment
	err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = u.repo.WithTx(tx).AddSelf(ctx, accountID.String(), tosVersion)
		if err != nil {
			return err
		}
		return u.setRole(ctx, tx, accountID, auth.AccountRolePendingAgent)
	})
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, enrollment.ErrEnrollmentPending) {
		return enrollment.Enrollment{}, false, err
	}

	live, gerr := u.repo.GetLatestByAccount(ctx, accountID.String())
	if gerr != nil {
		return enrollment.Enrollment{}, false, gerr
	}
	if !live.SelfServe() || live.Status != enrollment.StatusAccepted {
		return enrollment.Enrollment{}, false, enrollment.ErrEnrollmentPending
	}

	return live, false, nil
}

// openGuarantee provisions the agent profile and opens the guarantee
// funding request. Both calls are idempotent, so a retried SelfEnroll
// converges instead of duplicating. City is only ever seeded here, at
// profile creation — an existing profile keeps the city it already has.
func (u UseCase) openGuarantee(ctx context.Context, accountID, enrollmentID uuid.UUID, city *string) error {
	profile, err := u.ensureAgentProfile(ctx, accountID, city)
	if err != nil {
		return fmt.Errorf("creating agent profile: %w", err)
	}

	if profile.GuaranteeFloor <= 0 {
		// Nothing is owed, so nothing will ever settle — and settlement is
		// this path's only route to agent. Surface it rather than leaving
		// the candidate to wait on a deposit that cannot exist.
		u.logger.Info(ctx, "Enrollment.SelfEnroll: guarantee floor is zero, no funding request opened", "account", accountID)
		return nil
	}

	// The candidate's own passport: they send the money and attach the
	// proof, so they are the accountable creator. AlreadyExists means an
	// earlier attempt already opened it — success.
	if _, err := u.moka.Payment.Ledger.CreateLedgerRequest(asPendingAgent(ctx), &paymentv1.CreateLedgerRequestRequest{
		Type:                paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_FUNDING,
		Amount:              profile.GuaranteeFloor,
		ReferenceEntityType: new(paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_ENROLLMENT),
		ReferenceEntityId:   new(enrollmentID.String()),
	}); err != nil && status.Code(err) != codes.AlreadyExists {
		return fmt.Errorf("opening guarantee funding request: %w", err)
	}

	return nil
}

// asPendingAgent re-stamps the caller's passport with the role their
// account now actually holds.
//
// This is not a privilege grant, it is a staleness fix, and only the
// self-serve path needs it: the gateway rehydrated the inbound passport
// while the caller was still a plain `user`, and SelfEnroll flips them to
// pending_agent a few lines earlier in the SAME request. Payment's
// CreateLedgerRequest authorizes the guarantee funding off that role, so
// forwarding the passport verbatim would have it judge a role the account
// stopped having one commit ago — it would reject a request the very next
// call, with a freshly rehydrated passport, would accept. The invited path
// never hits this: an invited candidate was flipped by an earlier request
// and arrives already pending_agent.
//
// Nothing but the role is touched, and the user service is the authority
// on roles — it is stating what it just committed.
func asPendingAgent(ctx context.Context) context.Context {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		// No passport to refresh; let the callee reject it on its own terms.
		return ctx
	}

	refreshed := *pass
	refreshed.User.Role = string(auth.AccountRolePendingAgent)

	return passport.AddToContext(ctx, &refreshed)
}

// SettleGuarantee is the self-serve path's exit, called by the payment
// service (system passport, no gateway route) once ops approves the
// guarantee deposit. That approval IS the decision — there is no separate
// admin verdict on a self-serve enrollment — so this replicates Approve's
// effect exactly: profile activated, request approved, account role flipped
// to agent on one transaction.
//
// An invited enrollment is left alone: its approval is still the admin's
// to make, and the ledger request it settles was approved BY that decision
// in the first place.
func (u UseCase) SettleGuarantee(ctx context.Context, enrollmentID, decidedBy uuid.UUID) (enrollment.Enrollment, error) {
	current, err := u.repo.Get(ctx, enrollmentID.String())
	if err != nil {
		return enrollment.Enrollment{}, err
	}
	if !current.SelfServe() || current.Status == enrollment.StatusApproved {
		return current, nil
	}
	if current.Status != enrollment.StatusAccepted {
		// declined/cancelled: the deposit settled against a dead request.
		return enrollment.Enrollment{}, enrollment.ErrEnrollmentNotFound
	}

	// Remote side first, outside the transaction — a remote call must
	// never hold row locks open. Idempotent, so a failed local commit
	// below just means the job retries.
	if err := u.activateAgentProfile(ctx, current.AccountID); err != nil {
		u.logger.Error(ctx, "Enrollment.SettleGuarantee: failed to activate agent profile", "err", err)
		return enrollment.Enrollment{}, err
	}

	approved, err := u.promote(ctx, enrollmentID, decidedBy)
	if err != nil {
		if !errors.Is(err, enrollment.ErrEnrollmentNotFound) && !errors.Is(err, enrollment.ErrNotEligible) {
			u.logger.Error(ctx, "Enrollment.SettleGuarantee: failed", "err", err)
		}
		return enrollment.Enrollment{}, err
	}

	u.notify(ctx, approved.AccountID, "enrollment.approved",
		"You are now a MOKA PAY agent",
		"Your guarantee deposit has been validated. Welcome aboard!")

	return approved, nil
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
	// No city on the invited path: nothing in that flow ever asks for one
	// (it is the self-serve form's field), so the profile keeps the
	// payment service's default.
	profile, err := u.ensureAgentProfile(ctx, accountID, nil)
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

	approved, err := u.promote(ctx, id, decidedBy)
	if err != nil {
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

// promote is the single place an enrollment becomes an agent: the status
// flip and the account's role change commit together, and a moderation
// strike landing mid-flow rolls the whole thing back. Both entry points go
// through it, so "approved" can never mean one without the other.
func (u UseCase) promote(ctx context.Context, id, decidedBy uuid.UUID) (enrollment.Enrollment, error) {
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
		return enrollment.Enrollment{}, err
	}

	return approved, nil
}

// ensureAgentProfile creates the candidate's money account, or resolves
// the existing one — both entry points need the profile either way (its
// guarantee floor sizes the funding request). City is a creation-time
// seed: an already-existing profile is returned untouched.
func (u UseCase) ensureAgentProfile(ctx context.Context, accountID uuid.UUID, city *string) (*paymentv1.AgentProfile, error) {
	sysCtx := passport.AddToContext(ctx, passport.SystemPassport())

	created, err := u.moka.Payment.Admin.CreateAgentProfile(sysCtx, &paymentv1.CreateAgentProfileRequest{
		AccountId: accountID.String(),
		City:      city,
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
