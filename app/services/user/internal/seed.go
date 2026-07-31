package internal

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/core/aws"
	"mokapay.app/core/client"
	"mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

// seedPassword is shared by every seeded test account below. Dev/test only
// — never assign it to a real account.
const seedPassword = "Changeme123!"

// testAccount is one fixed local/dev login the team seeds everywhere so
// nobody has to provision test users by hand.
type testAccount struct {
	Phone     string
	FirstName string
	LastName  string
	Role      auth.AccountRole
}

// testAccounts are provisioned by Seed, in order. Fixed phone numbers +
// shared password so every environment (local, CI, a teammate's machine)
// logs in the same way. The numbers come from business/auth, which is also
// what makes their OTP fixed and keeps them off the real SMS provider.
var testAccounts = []testAccount{
	{Phone: auth.TestPhoneAdmin, FirstName: "Moka", LastName: "Admin", Role: auth.AccountRoleAdmin},
	{Phone: auth.TestPhoneUser, FirstName: "Moka", LastName: "User", Role: auth.AccountRoleUser},
	{Phone: auth.TestPhoneAgent, FirstName: "Moka", LastName: "Agent", Role: auth.AccountRoleAgent},
}

// Seed provisions the fixed test accounts above (Cognito identity + account
// row), and gives the seeded agent a live payment-side profile so it isn't
// just a role with nothing behind it. No env var required — the accounts
// and their password are fixed, dev/test data. Idempotent: an account
// already on its seed phone is left as-is (Cognito is never re-touched).
func Seed() error {
	ctx := context.Background()
	cfg := newConfig()
	log := newLogger()

	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "seed", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	cognito, err := aws.NewCognito(ctx, cfg.Cognito.ClientID, cfg.Cognito.ClientSecret, cfg.Cognito.PoolID, cfg.Cognito.Region)
	if err != nil {
		log.Error(ctx, "seed", "failed to init cognito client", "error", err)
		return err
	}

	accounts := repo.New(pg)

	var agentAccountID uuid.UUID
	var agentSeeded bool

	for _, ta := range testAccounts {
		id, seeded, err := seedAccount(ctx, log, accounts, cognito, ta)
		if err != nil {
			return err
		}
		if ta.Role == auth.AccountRoleAgent && seeded {
			agentAccountID, agentSeeded = id, true
		}
	}

	if agentSeeded {
		ensureAgentProfile(ctx, log, cfg, agentAccountID)
	}

	return nil
}

// seedAccount creates one test account (Cognito user + account row) unless
// its phone is already taken. Returns the account id and whether it now
// exists (created here or already present) — false only on error.
func seedAccount(ctx context.Context, log *logger.Logger, accounts repo.Store, cognito *aws.Cognito, ta testAccount) (uuid.UUID, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	existing, err := accounts.GetAccountByPhone(ctx, ta.Phone)
	if err == nil {
		log.Info(ctx, "seed", "status", "test account already present", "phone", ta.Phone, "role", ta.Role)
		return existing.ID, true, nil
	}
	if !errors.Is(err, auth.ErrAccountNotFound) {
		log.Error(ctx, "seed", "failed to check for existing test account", "error", err, "phone", ta.Phone)
		return uuid.UUID{}, false, err
	}

	// Cognito identity first — a row insert failure below just leaves an
	// unused Cognito user (SignUp is not re-run once the phone is taken),
	// same compensation trade-off as registration/staff creation elsewhere
	// in this package. The phone may already be provisioned in Cognito
	// out-of-band (no matching account row yet) — recover its sub and pin
	// the known seed password rather than failing.
	sub, err := cognito.SignUp(ctx, ta.Phone, seedPassword)
	if err != nil {
		if !errors.Is(err, aws.ErrPhoneTaken) {
			log.Error(ctx, "seed", "failed to provision cognito user", "error", err, "phone", ta.Phone)
			return uuid.UUID{}, false, err
		}

		log.Info(ctx, "seed", "status", "cognito user already present, reusing", "phone", ta.Phone)

		// Pin the known seed password regardless of how the Cognito user
		// was provisioned, so the documented test credentials always work —
		// this also lets the Login just below succeed unconditionally.
		if err := cognito.ResetPassword(ctx, ta.Phone, seedPassword); err != nil && !errors.Is(err, aws.ErrFailToLogOut) {
			log.Error(ctx, "seed", "failed to pin seed password on existing cognito user", "error", err, "phone", ta.Phone)
			return uuid.UUID{}, false, err
		}

		// AdminGetUser isn't in this IAM policy's allowed action set, so the
		// sub is recovered the way every other caller learns it: log in and
		// read the "sub" claim off the token Cognito hands back.
		accessToken, _, _, err := cognito.Login(ctx, ta.Phone, seedPassword)
		if err != nil {
			log.Error(ctx, "seed", "failed to log into existing cognito user to resolve its sub", "error", err, "phone", ta.Phone)
			return uuid.UUID{}, false, err
		}
		sub, err = aws.SubFromAccessToken(accessToken)
		if err != nil {
			log.Error(ctx, "seed", "failed to resolve existing cognito user", "error", err, "phone", ta.Phone)
			return uuid.UUID{}, false, err
		}
	}

	created, err := accounts.AddStaffAccount(ctx, ta.Role, auth.AccountStatusActive, auth.NewAccount{
		PhoneNumber: ta.Phone,
		FirstName:   ta.FirstName,
		LastName:    ta.LastName,
	}, sub)
	if err != nil {
		log.Error(ctx, "seed", "failed to insert test account row — cognito user orphaned, needs manual cleanup", "error", err, "phone", ta.Phone)
		return uuid.UUID{}, false, err
	}

	log.Info(ctx, "seed", "status", "test account created", "phone", ta.Phone, "role", ta.Role)
	return created.ID, true, nil
}

// ensureAgentProfile gives the seeded agent test account a live money
// account on the payment side — the same CreateAgentProfile/Activate pair
// the enrollment approval flow drives — otherwise the account logs in with
// role agent but every payment-side screen 404s on it. Best-effort: the
// payment service may not be reachable yet (e.g. `make user/seed` run
// before payment is up), logged but never fatal to the rest of seeding.
func ensureAgentProfile(ctx context.Context, log *logger.Logger, cfg Config, accountID uuid.UUID) {
	moka, err := client.NewMokaClient(client.Config{PaymentServiceAddr: cfg.Client.PaymentGRPCPort}, nil)
	if err != nil {
		log.Error(ctx, "seed", "failed to build payment client, skipping agent profile", "error", err)
		return
	}
	defer func() {
		if err := moka.Close(ctx); err != nil {
			log.Error(ctx, "seed", "failed to close payment client", "error", err)
		}
	}()

	sctx, cancel := context.WithTimeout(passport.AddToContext(ctx, passport.SystemPassport()), 10*time.Second)
	defer cancel()

	profile, err := moka.Payment.Admin.CreateAgentProfile(sctx, &paymentv1.CreateAgentProfileRequest{AccountId: accountID.String()})
	if err != nil {
		if status.Code(err) != codes.AlreadyExists {
			log.Error(ctx, "seed", "failed to create agent profile — payment service may not be up yet, agent test account has no wallet", "error", err)
			return
		}
		profile, err = moka.Payment.Admin.GetAgentProfile(sctx, &paymentv1.GetAgentProfileRequest{
			Selector: &paymentv1.GetAgentProfileRequest_AccountId{AccountId: accountID.String()},
		})
		if err != nil {
			log.Error(ctx, "seed", "failed to resolve existing agent profile", "error", err)
			return
		}
	}

	if _, err := moka.Payment.Admin.ActivateAgentProfile(sctx, &paymentv1.ActivateAgentProfileRequest{Id: profile.Id}); err != nil {
		log.Error(ctx, "seed", "failed to activate agent profile", "error", err)
		return
	}

	log.Info(ctx, "seed", "status", "agent profile ready", "account", accountID)
}
