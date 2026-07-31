// Package usecase owns the client side of the transfer flow: the
// stateless quote, creation (the one place the rate locks and every
// money value derives), owner-scoped reads and the beneficiary book.
// All arithmetic is integer — rates travel as micros, fees as basis
// points; nothing passes through a float.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	walletrepo "mokapay.app/app/services/payment/internal/business/agentwallet/repo"
	"mokapay.app/app/services/payment/internal/business/beneficiary"
	benrepo "mokapay.app/app/services/payment/internal/business/beneficiary/repo"
	"mokapay.app/app/services/payment/internal/business/commission"
	commissionrepo "mokapay.app/app/services/payment/internal/business/commission/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/app/services/payment/internal/business/transfer/repo"
	"mokapay.app/app/services/payment/internal/integration/fxrate"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	"mokapay.app/core/ws"
)

// Jobs is the River publisher the usecase enqueues through — always in
// the SAME transaction as the state change (a committed transfer always
// has its match job; a payout proof always has its auto-confirm clock).
// Implemented by the events package.
type Jobs interface {
	ScheduleMatch(ctx context.Context, tx pgx.Tx, transferID uuid.UUID) error
	SchedulePayoutTimeout(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error
	ScheduleAutoConfirm(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error
	ScheduleComplianceEvaluation(ctx context.Context, tx pgx.Tx, transferID uuid.UUID) error
	Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error
}

// Live is the websocket sink: the same transition the notification job
// carries, pushed to whatever sockets the parties have open right now. A
// second sink at the same call site, never a second signal path — and
// best-effort, so a down event bus costs a screen its instant refresh,
// never the transition itself (the notification job and the backstop poll
// stay authoritative).
type Live interface {
	Publish(ctx context.Context, accountID string, event ws.Event) error
}

// Config carries the money knobs, all env-resolved. Fees are basis
// points (150 = 1.50%); MaxAmount caps from_amount in its own minor
// units and keeps the integer math overflow-safe.
type Config struct {
	FeeSameBps       int32
	FeeFXBps         int32
	MarginBps        int32
	MaxAmount        int64
	PayoutTimeout    time.Duration
	AutoConfirmAfter time.Duration
}

type UseCase struct {
	logger        *logger.Logger
	db            *coredb.Postgres
	transfers     repo.Store
	beneficiaries benrepo.Store
	wallets       walletrepo.Store
	profiles      profilerepo.Store
	commissions   commissionrepo.Store
	fx            fxrate.Provider
	jobs          Jobs
	live          Live
	cfg           Config
}

func New(l *logger.Logger, db *coredb.Postgres, fx fxrate.Provider, jobs Jobs, live Live, cfg Config) UseCase {
	return UseCase{
		logger:        l,
		db:            db,
		transfers:     repo.New(db),
		beneficiaries: benrepo.New(db),
		wallets:       walletrepo.New(db),
		profiles:      profilerepo.New(db),
		commissions:   commissionrepo.New(db),
		fx:            fx,
		jobs:          jobs,
		live:          live,
		cfg:           cfg,
	}
}

// Quote is the stateless quotation — nothing persisted; the rate locks
// at Create. RateMicros is nil on same-currency corridors.
type Quote struct {
	FromCurrency transfer.Currency
	FromAmount   int64
	ToCurrency   transfer.Currency
	ToAmount     int64
	FeePctBps    int32
	FeeAmount    int64
	MarginAmount int64
	TotalAmount  int64
	RateMicros   *int64
	HoldAmount   int64 // USD minor units — what accepting will reserve
}

// NewRecipient are inline recipient details; creating with them also
// saves/refreshes the beneficiary.
type NewRecipient struct {
	Name     string
	Phone    string
	Operator transfer.Operator
	City     *string
}

// CreateTransfer is the create input; exactly one of BeneficiaryID /
// Recipient must be set.
type CreateTransfer struct {
	FromCurrency  transfer.Currency
	FromOperator  transfer.Operator
	FromAmount    int64
	ToCurrency    transfer.Currency
	BeneficiaryID *uuid.UUID
	Recipient     *NewRecipient
	Note          *string
}

// TransferView is a transfer plus, for its owner once matched, the
// payment details of the agent's wallet on the {from_operator,
// from_currency} pair.
type TransferView struct {
	transfer.Transfer
	PaymentDetails *agentwallet.Wallet
}

// AvailableOperators returns the v1 catalogue after validating the
// corridor (all three operators serve both currencies).
func (u UseCase) AvailableOperators(from, to transfer.Currency) ([]transfer.Operator, error) {
	if err := validateCorridor(from, to); err != nil {
		return nil, err
	}
	return transfer.Operators, nil
}

// GetQuote computes a quote from the cached rate.
func (u UseCase) GetQuote(ctx context.Context, from transfer.Currency, amount int64, to transfer.Currency) (Quote, error) {
	return u.quote(ctx, from, amount, to)
}

// GetQuoteByTarget is the reverse widget direction: the recipient must
// receive target (to-currency minor units) — solve for the smallest
// from_amount whose quote covers it, and return THAT quote. Stateless
// like the forward path; the response's to_amount may exceed the target
// by a rounding sliver (integer cents can't hit every franc exactly).
func (u UseCase) GetQuoteByTarget(ctx context.Context, from transfer.Currency, to transfer.Currency, target int64) (Quote, error) {
	if err := validateCorridor(from, to); err != nil {
		return Quote{}, err
	}
	if target <= 0 {
		return Quote{}, transfer.ErrInvalidAmount
	}

	// Same-currency corridors: the recipient receives exactly from_amount
	// (the fee rides on top of it).
	if from == to {
		return u.quote(ctx, from, target, to)
	}

	// usd → cdf. Invert to = gross − margin, gross = from×rate/1e8:
	// estimate, then verify with the authoritative forward quote and nudge
	// up if integer rounding left it a franc short (a cent is worth
	// thousands of francs of estimate error at most once).
	rate, err := u.fx.RateMicros(ctx)
	if err != nil {
		u.logger.Error(ctx, "Transfer.QuoteByTarget: rate lookup failed", "err", err)
		return Quote{}, fmt.Errorf("resolving usd:cdf rate: %w", err)
	}
	grossNeeded := target * 10_000 / int64(10_000-u.cfg.MarginBps)
	// float64 keeps the estimate within a fraction of a cent here, and the
	// int64 form grossNeeded×1e8 could overflow for large targets. Start
	// one cent BELOW the floor estimate and walk up: the margin rounds
	// half-up (not ceil), so the true answer can sit under the estimate —
	// walking up from below finds the smallest covering amount.
	amount := int64(math.Floor(float64(grossNeeded)*100_000_000/float64(rate))) - 1
	if amount < 1 {
		amount = 1
	}

	for range 6 {
		q, err := u.quote(ctx, from, amount, to)
		if err != nil {
			return Quote{}, err
		}
		if q.ToAmount >= target {
			return q, nil
		}
		amount++
	}

	// Six cents of correction is far beyond any rounding drift — a rate
	// change mid-loop is the only way here; the caller just retries.
	return Quote{}, transfer.ErrInvalidAmount
}

// CreateTransfer resolves the recipient, locks the rate, derives every
// money value and lands the transfer + its match job in one transaction.
// clientID comes from the passport — an explicit parameter, never
// payload data.
func (u UseCase) CreateTransfer(ctx context.Context, clientID uuid.UUID, in CreateTransfer) (transfer.Transfer, error) {
	q, err := u.quote(ctx, in.FromCurrency, in.FromAmount, in.ToCurrency)
	if err != nil {
		return transfer.Transfer{}, err
	}

	// Resolve the recipient: a saved beneficiary (owner-checked) or
	// inline details (which upsert the book).
	var ben beneficiary.Beneficiary
	switch {
	case in.BeneficiaryID != nil:
		ben, err = u.beneficiaries.Get(ctx, in.BeneficiaryID.String())
		if err != nil {
			return transfer.Transfer{}, err
		}
		if ben.ClientID != clientID {
			// A foreign beneficiary is indistinguishable from a missing one.
			return transfer.Transfer{}, beneficiary.ErrBeneficiaryNotFound
		}
	case in.Recipient != nil:
		ben, err = u.beneficiaries.Add(ctx, clientID, beneficiary.NewBeneficiary{
			Name:     in.Recipient.Name,
			Phone:    in.Recipient.Phone,
			Operator: in.Recipient.Operator,
			City:     in.Recipient.City,
		})
		if err != nil {
			u.logger.Error(ctx, "Transfer.Create: saving beneficiary", "err", err)
			return transfer.Transfer{}, err
		}
	default:
		return transfer.Transfer{}, transfer.ErrRecipientRequired
	}

	nt := transfer.NewTransfer{
		BeneficiaryID:     new(ben.ID),
		RecipientName:     ben.Name,
		RecipientPhone:    ben.Phone,
		RecipientOperator: ben.Operator,
		RecipientCity:     ben.City,
		FromCurrency:      q.FromCurrency,
		FromOperator:      in.FromOperator,
		FromAmount:        q.FromAmount,
		ToCurrency:        q.ToCurrency,
		ToAmount:          q.ToAmount,
		FeePctBps:         q.FeePctBps,
		FeeAmount:         q.FeeAmount,
		MarginAmount:      q.MarginAmount,
		HoldAmount:        q.HoldAmount,
		RateMicros:        q.RateMicros,
		Note:              in.Note,
	}

	var created transfer.Transfer
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		created, err = u.transfers.WithTx(tx).Add(ctx, clientID, nt)
		if err != nil {
			return err
		}
		// The match job commits with the transfer — matching starts the
		// moment the row exists, no polling.
		return u.jobs.ScheduleMatch(ctx, tx, created.ID)
	}); err != nil {
		u.logger.Error(ctx, "Transfer.Create: failed", "err", err)
		return transfer.Transfer{}, fmt.Errorf("creating transfer: %w", err)
	}

	return created, nil
}

// Get returns the transfer if the passport may see it: its owner
// (payment details attached once matched), the assigned agent, or
// ops/admin/system.
func (u UseCase) Get(ctx context.Context, pass *passport.Passport, id uuid.UUID) (TransferView, error) {
	t, err := u.transfers.Get(ctx, id.String())
	if err != nil {
		return TransferView{}, err
	}

	role, callerID, err := callerIdentity(pass)
	if err != nil {
		return TransferView{}, err
	}

	switch {
	case role == "system" || role == "ops" || role == "admin":
		return TransferView{Transfer: t}, nil
	case t.ClientID == callerID:
		return u.withPaymentDetails(ctx, t), nil
	case t.AgentProfileID != nil:
		profile, perr := u.profiles.Get(ctx, t.AgentProfileID.String())
		if perr == nil && profile.AccountID == callerID {
			return TransferView{Transfer: t}, nil
		}
	}

	// Not yours: indistinguishable from missing.
	return TransferView{}, transfer.ErrTransferNotFound
}

// ListForClient returns the caller's transfers, newest first.
func (u UseCase) ListForClient(ctx context.Context, clientID uuid.UUID, status *transfer.Status, limit int32) ([]transfer.Transfer, error) {
	return u.transfers.List(ctx, transfer.Filter{ClientID: new(clientID), Status: status}, defaultLimit(limit))
}

// ListForAgent returns the transfers assigned to the agent profile.
func (u UseCase) ListForAgent(ctx context.Context, agentProfileID uuid.UUID, status *transfer.Status, limit int32) ([]transfer.Transfer, error) {
	return u.transfers.List(ctx, transfer.Filter{AgentProfileID: new(agentProfileID), Status: status}, defaultLimit(limit))
}

// ListAll is the ops listing (optional status filter).
func (u UseCase) ListAll(ctx context.Context, status *transfer.Status, limit int32) ([]transfer.Transfer, error) {
	return u.transfers.List(ctx, transfer.Filter{Status: status}, defaultLimit(limit))
}

// Timeline returns the transfer's event history after the same
// visibility check as Get.
func (u UseCase) Timeline(ctx context.Context, pass *passport.Passport, id uuid.UUID) ([]transfer.Event, error) {
	if _, err := u.Get(ctx, pass, id); err != nil {
		return nil, err
	}
	return u.transfers.ListEvents(ctx, id.String())
}

// ListBeneficiaries returns the caller's saved recipients.
func (u UseCase) ListBeneficiaries(ctx context.Context, clientID uuid.UUID) ([]beneficiary.Beneficiary, error) {
	return u.beneficiaries.List(ctx, clientID)
}

// AddBeneficiary saves a recipient ahead of any transfer — the same
// upsert CreateTransfer performs for inline recipients: re-adding a live
// (client, phone) pair refreshes its details instead of erroring.
func (u UseCase) AddBeneficiary(ctx context.Context, clientID uuid.UUID, nb beneficiary.NewBeneficiary) (beneficiary.Beneficiary, error) {
	b, err := u.beneficiaries.Add(ctx, clientID, nb)
	if err != nil {
		u.logger.Error(ctx, "Transfer.AddBeneficiary: failed", "err", err)
		return beneficiary.Beneficiary{}, err
	}
	return b, nil
}

// UpdateBeneficiary edits one of the caller's saved recipients; nil
// fields keep their value. The transfer keeps its own recipient
// snapshot, so past history never changes.
func (u UseCase) UpdateBeneficiary(ctx context.Context, clientID, id uuid.UUID, ub beneficiary.UpdateBeneficiary) (beneficiary.Beneficiary, error) {
	b, err := u.beneficiaries.Update(ctx, id.String(), clientID, ub)
	if err != nil {
		if !errors.Is(err, beneficiary.ErrBeneficiaryNotFound) && !errors.Is(err, beneficiary.ErrBeneficiaryExists) {
			u.logger.Error(ctx, "Transfer.UpdateBeneficiary: failed", "err", err)
		}
		return beneficiary.Beneficiary{}, err
	}
	return b, nil
}

// DeleteBeneficiary removes one of the caller's saved recipients.
func (u UseCase) DeleteBeneficiary(ctx context.Context, clientID, id uuid.UUID) error {
	return u.beneficiaries.Delete(ctx, id.String(), clientID)
}

// ApplyProof is the file worker's entry (system-only at the RPC layer):
// forward-only between the two proof-gated moves, never to done. A
// redelivered proof (zero rows) is idempotent — the current row comes
// back unchanged. The payout proof arms the auto-confirm clock in the
// same commit.
func (u UseCase) ApplyProof(ctx context.Context, id uuid.UUID, newStatus transfer.Status, proofID uuid.UUID) (transfer.Transfer, error) {
	if newStatus != transfer.StatusPendingAgentTransfer && newStatus != transfer.StatusPendingClientConfirmation {
		return transfer.Transfer{}, transfer.ErrInvalidStatusMove
	}

	var updated transfer.Transfer
	err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		store := u.transfers.WithTx(tx)
		var terr error
		if newStatus == transfer.StatusPendingAgentTransfer {
			updated, terr = store.MarkClientPaid(ctx, id.String(), proofID, nil)
			if terr != nil {
				return terr
			}
			// The client's money has left their hands: the agent now has a
			// window to pay out, and missing it auto-disputes rather than
			// cancels (the hold must stay put — somebody IS out money now).
			if err := u.jobs.SchedulePayoutTimeout(ctx, tx, updated.ID, time.Now().Add(u.cfg.PayoutTimeout)); err != nil {
				return err
			}
			agent, aerr := u.profiles.Get(ctx, updated.AgentProfileID.String())
			if aerr != nil {
				return aerr
			}
			return u.jobs.Notify(ctx, tx, agent.AccountID.String(),
				transfer.EventClientPaid,
				"Paiement client reçu",
				fmt.Sprintf("Le client du transfert %s a payé — effectuez le versement.", updated.Reference))
		}

		updated, terr = store.MarkAgentPaid(ctx, id.String(), proofID, nil)
		if terr != nil {
			return terr
		}
		if err := u.jobs.ScheduleAutoConfirm(ctx, tx, updated.ID, time.Now().Add(u.cfg.AutoConfirmAfter)); err != nil {
			return err
		}
		return u.jobs.Notify(ctx, tx, updated.ClientID.String(),
			transfer.EventPayoutDone,
			"Versement effectué",
			fmt.Sprintf("Le destinataire du transfert %s a été payé — confirmez la réception.", updated.Reference))
	})
	if err != nil {
		if errors.Is(err, transfer.ErrTransferNotFound) {
			// Redelivered proof event: read back — if the move already
			// happened, report it as success (idempotent for the worker).
			current, gerr := u.transfers.Get(ctx, id.String())
			if gerr == nil && alreadyPast(current.Status, newStatus) {
				return current, nil
			}
			return transfer.Transfer{}, transfer.ErrTransferNotFound
		}
		u.logger.Error(ctx, "Transfer.ApplyProof: failed", "err", err)
		return transfer.Transfer{}, err
	}

	// The proof landed: this is the transition the whole websocket effort is
	// about — the party that did NOT upload has a screen sitting on the old
	// status until something tells it otherwise.
	event := transfer.EventClientPaid
	if newStatus == transfer.StatusPendingClientConfirmation {
		event = transfer.EventPayoutDone
	}
	u.pushLive(ctx, updated, event)

	return updated, nil
}

// Confirm is the client's receipt confirmation — the only human path to
// done; releases the agent's hold and accrues their commission in the
// same commit.
func (u UseCase) Confirm(ctx context.Context, clientID, id uuid.UUID) (transfer.Transfer, error) {
	t, err := u.transfers.Get(ctx, id.String())
	if err != nil {
		return transfer.Transfer{}, err
	}
	if t.ClientID != clientID {
		return transfer.Transfer{}, transfer.ErrTransferNotFound
	}

	var done transfer.Transfer
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		done, err = u.transfers.WithTx(tx).Complete(ctx, id.String(), new(clientID), nil)
		if err != nil {
			return err
		}
		if err := u.onCompleted(ctx, tx, done); err != nil {
			return err
		}
		agent, aerr := u.profiles.Get(ctx, done.AgentProfileID.String())
		if aerr != nil {
			return aerr
		}
		return u.jobs.Notify(ctx, tx, agent.AccountID.String(),
			transfer.EventCompleted,
			"Transfert terminé",
			fmt.Sprintf("Le transfert %s est confirmé — votre garantie est libérée.", done.Reference))
	}); err != nil {
		if !errors.Is(err, transfer.ErrTransferNotFound) {
			u.logger.Error(ctx, "Transfer.Confirm: failed", "err", err)
		}
		return transfer.Transfer{}, err
	}

	u.pushLive(ctx, done, transfer.EventCompleted)

	return done, nil
}

// CancelByClient: the owner backs out before an agent committed.
func (u UseCase) CancelByClient(ctx context.Context, clientID, id uuid.UUID, reason *string) (transfer.Transfer, error) {
	cancelled, err := u.transfers.CancelPreAccept(ctx, id.String(), clientID, reason)
	if err != nil {
		if !errors.Is(err, transfer.ErrTransferNotFound) {
			u.logger.Error(ctx, "Transfer.CancelByClient: failed", "err", err)
		}
		return transfer.Transfer{}, err
	}
	return cancelled, nil
}

// CancelByAdmin: the ops escape hatch from any open state, releasing
// the hold when placed; both parties are told in the same commit.
func (u UseCase) CancelByAdmin(ctx context.Context, adminID, id uuid.UUID, reason string) (transfer.Transfer, error) {
	var cancelled transfer.Transfer
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var terr error
		cancelled, terr = u.transfers.WithTx(tx).CancelAdmin(ctx, id.String(), adminID, reason)
		if terr != nil {
			return terr
		}
		if err := u.jobs.Notify(ctx, tx, cancelled.ClientID.String(),
			transfer.EventCancelled, "Transfert annulé",
			fmt.Sprintf("Votre transfert %s a été annulé: %s", cancelled.Reference, reason)); err != nil {
			return err
		}
		if cancelled.AgentProfileID != nil {
			agent, aerr := u.profiles.Get(ctx, cancelled.AgentProfileID.String())
			if aerr != nil {
				return aerr
			}
			return u.jobs.Notify(ctx, tx, agent.AccountID.String(),
				transfer.EventCancelled, "Transfert annulé",
				fmt.Sprintf("Le transfert %s a été annulé: %s", cancelled.Reference, reason))
		}
		return nil
	}); err != nil {
		if !errors.Is(err, transfer.ErrTransferNotFound) {
			u.logger.Error(ctx, "Transfer.CancelByAdmin: failed", "err", err)
		}
		return transfer.Transfer{}, err
	}

	u.pushLive(ctx, cancelled, transfer.EventCancelled)

	return cancelled, nil
}

// HandlePaymentTimeout is the timer's exit when the client never paid:
// cancel + release, both parties told. A moved-on transfer is a no-op.
func (u UseCase) HandlePaymentTimeout(ctx context.Context, id uuid.UUID) error {
	var cancelled transfer.Transfer
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var err error
		cancelled, err = u.transfers.WithTx(tx).CancelTimeout(ctx, id.String(), "payment window elapsed")
		if err != nil {
			if errors.Is(err, transfer.ErrTransferNotFound) {
				return nil // the proof arrived first — the clock is stale
			}
			return err
		}
		if err := u.jobs.Notify(ctx, tx, cancelled.ClientID.String(),
			transfer.EventCancelled, "Transfert annulé",
			fmt.Sprintf("Votre transfert %s a été annulé: paiement non reçu à temps.", cancelled.Reference)); err != nil {
			return err
		}
		agent, aerr := u.profiles.Get(ctx, cancelled.AgentProfileID.String())
		if aerr != nil {
			return aerr
		}
		return u.jobs.Notify(ctx, tx, agent.AccountID.String(),
			transfer.EventCancelled, "Transfert annulé",
			fmt.Sprintf("Le transfert %s a expiré sans paiement — votre garantie est libérée.", cancelled.Reference))
	}); err != nil {
		u.logger.Error(ctx, "Transfer.HandlePaymentTimeout: failed", "err", err)
		return err
	}

	// A stale clock cancelled nothing: no transition, nothing to announce.
	if cancelled.ID != uuid.Nil {
		u.pushLive(ctx, cancelled, transfer.EventCancelled)
	}

	return nil
}

// HandleAutoConfirm completes on the client's behalf after the
// confirmation window (D11). A moved-on transfer is a no-op.
func (u UseCase) HandleAutoConfirm(ctx context.Context, id uuid.UUID) error {
	reason := "auto-confirmed after inactivity"
	var done transfer.Transfer
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		var err error
		done, err = u.transfers.WithTx(tx).Complete(ctx, id.String(), nil, new(reason))
		if err != nil {
			if errors.Is(err, transfer.ErrTransferNotFound) {
				return nil // confirmed (or cancelled) meanwhile
			}
			return err
		}
		if err := u.onCompleted(ctx, tx, done); err != nil {
			return err
		}
		agent, aerr := u.profiles.Get(ctx, done.AgentProfileID.String())
		if aerr != nil {
			return aerr
		}
		return u.jobs.Notify(ctx, tx, agent.AccountID.String(),
			transfer.EventCompleted, "Transfert terminé",
			fmt.Sprintf("Le transfert %s est confirmé — votre garantie est libérée.", done.Reference))
	}); err != nil {
		u.logger.Error(ctx, "Transfer.HandleAutoConfirm: failed", "err", err)
		return err
	}

	// A stale timer completed nothing (the client confirmed first).
	if done.ID != uuid.Nil {
		u.pushLive(ctx, done, transfer.EventCompleted)
	}

	return nil
}

// pushLive is the live half of a transition: the transfer's watchers get
// told to refetch it the moment the move commits, instead of learning on
// their next poll cycle.
//
// It goes to BOTH parties, where the notification job addresses only the
// one that has to act: the event is a cache invalidation ("this transfer
// moved"), and both screens are showing the same row — the client watching
// their own proof land is as stuck on a stale view as the agent waiting for
// it. Data carries the ids a refetch needs, never the transfer itself.
//
// Called AFTER the commit on purpose: an event whose refetch would still
// read the pre-commit row is worse than no event at all. Best-effort by
// design — a failed push is logged and dropped.
func (u UseCase) pushLive(ctx context.Context, t transfer.Transfer, eventType string) {
	if u.live == nil {
		return
	}

	for _, account := range u.watchers(ctx, t) {
		if err := u.live.Publish(ctx, account.String(), ws.Event{
			Type: eventType,
			Data: map[string]string{
				"transfer_id": t.ID.String(),
				"reference":   t.Reference,
			},
		}); err != nil {
			u.logger.Error(ctx, "Transfer.pushLive: live push failed", "event", eventType, "err", err)
		}
	}
}

// watchers are the accounts with a screen on this transfer: its owner, plus
// the assigned agent's account once one is assigned. A profile that cannot
// be read costs the agent their push, never the client theirs.
func (u UseCase) watchers(ctx context.Context, t transfer.Transfer) []uuid.UUID {
	accounts := []uuid.UUID{t.ClientID}
	if t.AgentProfileID == nil {
		return accounts
	}

	agent, err := u.profiles.Get(ctx, t.AgentProfileID.String())
	if err != nil {
		u.logger.Error(ctx, "Transfer.pushLive: agent lookup failed", "err", err)
		return accounts
	}

	return append(accounts, agent.AccountID)
}

// onCompleted books everything that rides a transfer reaching done, inside
// the transaction that completed it: the agent's commission accrual and
// the AML evaluation of the movement.
//
// The evaluation is a job, never a call: it commits with the completion
// and runs afterwards, so compliance can be slow, down or wrong without any
// of it reaching the client who just confirmed their transfer.
func (u UseCase) onCompleted(ctx context.Context, tx pgx.Tx, t transfer.Transfer) error {
	if err := u.accrue(ctx, tx, t); err != nil {
		return err
	}

	return u.jobs.ScheduleComplianceEvaluation(ctx, tx, t.ID)
}

// accrue books the agent's commission for a transfer that just reached
// done, inside the transaction that completed it — the accrual can never
// exist without the completion, nor the completion without the accrual.
// An already-accrued transfer is success, not an error: the UNIQUE on
// transfer_fk is the entire double-booking guard, which is why this
// service needs no commission backfill script.
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

// alreadyPast reports whether current sits at or beyond the target of a
// replayed proof move (the happy-path order).
func alreadyPast(current, target transfer.Status) bool {
	order := map[transfer.Status]int{
		transfer.StatusPendingMatch:              0,
		transfer.StatusPendingAgentApproval:      1,
		transfer.StatusPendingClientTransfer:     2,
		transfer.StatusPendingAgentTransfer:      3,
		transfer.StatusPendingClientConfirmation: 4,
		transfer.StatusDone:                      5,
	}
	c, okC := order[current]
	tgt, okT := order[target]
	return okC && okT && c >= tgt
}

// quote derives every money value with integer math:
//   - fee (from_currency): half-up on FeeSameBps / FeeFXBps
//   - cross-currency: gross_cdf = from × rate / 1e8 (cents×micros→francs),
//     margin half-up on MarginBps, to = gross − margin
//   - hold (USD minor units): the FULL client total incl. fee (settled
//     2026-07-25); CDF sources convert at the same rate, rounded UP —
//     the reservation never under-covers.
//
// The rate is fetched whenever FX applies OR the source is CDF (the
// hold is USD regardless of corridor).
func (u UseCase) quote(ctx context.Context, from transfer.Currency, amount int64, to transfer.Currency) (Quote, error) {
	if err := validateCorridor(from, to); err != nil {
		return Quote{}, err
	}
	if amount <= 0 || amount > u.cfg.MaxAmount {
		return Quote{}, transfer.ErrInvalidAmount
	}

	hasFX := from != to
	feeBps := u.cfg.FeeSameBps
	if hasFX {
		feeBps = u.cfg.FeeFXBps
	}
	fee := (amount*int64(feeBps) + 5_000) / 10_000
	total := amount + fee

	q := Quote{
		FromCurrency: from,
		FromAmount:   amount,
		ToCurrency:   to,
		ToAmount:     amount,
		FeePctBps:    feeBps,
		FeeAmount:    fee,
		TotalAmount:  total,
		HoldAmount:   total,
	}

	if !hasFX && from == transfer.CurrencyUSD {
		return q, nil // usd→usd needs no rate at all
	}

	rate, err := u.fx.RateMicros(ctx)
	if err != nil {
		u.logger.Error(ctx, "Transfer.Quote: rate lookup failed", "err", err)
		return Quote{}, fmt.Errorf("resolving usd:cdf rate: %w", err)
	}

	if hasFX { // usd → cdf
		gross := amount * rate / 100_000_000 // cents × micros → whole francs
		margin := (gross*int64(u.cfg.MarginBps) + 5_000) / 10_000
		q.ToAmount = gross - margin
		q.MarginAmount = margin
		q.RateMicros = new(rate)
		if q.ToAmount <= 0 {
			return Quote{}, transfer.ErrInvalidAmount
		}
	} else { // cdf → cdf: the rate serves the hold only, not the client
		q.HoldAmount = (total*100_000_000 + rate - 1) / rate // francs → cents, ceil
	}

	return q, nil
}

func (u UseCase) withPaymentDetails(ctx context.Context, t transfer.Transfer) TransferView {
	view := TransferView{Transfer: t}
	if t.AgentProfileID == nil {
		return view
	}
	switch t.Status {
	case transfer.StatusPendingClientTransfer, transfer.StatusPendingAgentTransfer,
		transfer.StatusPendingClientConfirmation, transfer.StatusDone:
	default:
		return view
	}

	w, err := u.wallets.GetByPair(ctx, *t.AgentProfileID, t.FromOperator, t.FromCurrency)
	if err != nil {
		// Display data only — a missing wallet degrades the view, never
		// the read.
		if !errors.Is(err, agentwallet.ErrWalletNotFound) {
			u.logger.Error(ctx, "Transfer.Get: payment details lookup failed", "err", err)
		}
		return view
	}
	view.PaymentDetails = new(w)

	return view
}

func callerIdentity(pass *passport.Passport) (role string, id uuid.UUID, err error) {
	if pass == nil {
		return "", uuid.UUID{}, transfer.ErrNotAllowed
	}
	if pass.IsSystem() {
		return "system", uuid.Nil, nil
	}
	id, perr := uuid.Parse(pass.User.ID)
	if perr != nil {
		return "", uuid.UUID{}, transfer.ErrNotAllowed
	}
	return pass.User.Role, id, nil
}

func validateCorridor(from, to transfer.Currency) error {
	valid := func(c transfer.Currency) bool {
		return c == transfer.CurrencyUSD || c == transfer.CurrencyCDF
	}
	if !valid(from) || !valid(to) {
		return transfer.ErrCorridorNotAllowed
	}
	if from == transfer.CurrencyCDF && to == transfer.CurrencyUSD {
		return transfer.ErrCorridorNotAllowed
	}
	return nil
}

func defaultLimit(limit int32) int32 {
	if limit <= 0 {
		return 50
	}
	return limit
}
