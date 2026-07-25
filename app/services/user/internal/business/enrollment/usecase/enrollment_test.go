package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/auth"
	authrepo "mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/business/enrollment/usecase"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/core/client"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

const tosVersion = "2026-07-25"

// fakeNotifier records the events pushed per account.
type fakeNotifier struct {
	events []string
}

func (f *fakeNotifier) Push(_ context.Context, nn notification.NewNotification) (notification.Notification, error) {
	f.events = append(f.events, nn.EventType)
	return notification.Notification{}, nil
}

// Cross-service integration tests: need a migrated users database (0003)
// AND a running payment service (the enrollment flow calls it through the
// Moka client — no fakes by design). Skipped unless both are set:
//
//	TEST_DB_PORT=5433 TEST_PAYMENT_GRPC_PORT=50053 \
//	  go test ./internal/business/enrollment/usecase/
func newTestUseCase(t *testing.T) (usecase.UseCase, *fakeNotifier, authrepo.Store, *client.MokaClient) {
	t.Helper()

	port := os.Getenv("TEST_DB_PORT")
	payPort := os.Getenv("TEST_PAYMENT_GRPC_PORT")
	if port == "" || payPort == "" {
		t.Skip("TEST_DB_PORT/TEST_PAYMENT_GRPC_PORT not set; integration test needs a migrated users DB and a running payment service")
	}

	db := coredb.New(coredb.PostgresConfig{
		Host:     getenv("TEST_DB_HOST", "localhost"),
		Port:     port,
		Username: getenv("TEST_DB_USER", "mokapay"),
		Password: getenv("TEST_DB_PASSWORD", "mokapay"),
		Database: getenv("TEST_DB_NAME", "users"),
	})
	if err := db.Open(); err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	moka, err := client.NewMokaClient(client.Config{
		PaymentServiceAddr: "localhost:" + payPort,
	}, map[client.Connection]bool{client.ConnectionPayment: true})
	if err != nil {
		t.Fatalf("building moka client: %v", err)
	}
	t.Cleanup(func() { _ = moka.Close(context.Background()) })

	notifier := &fakeNotifier{}
	log := logger.New(os.Stderr, logger.LevelInfo, "test", nil)
	return usecase.New(log, db, notifier, moka, tosVersion), notifier, authrepo.New(db), moka
}

// opsCtx fabricates the ops passport an approving admin carries — the
// payment service trusts forwarded passports (internal network).
func opsCtx(ctx context.Context, adminID uuid.UUID) context.Context {
	return passport.AddToContext(ctx, &passport.Passport{
		User: passport.User{ID: adminID.String(), Role: "ops"},
		Type: passport.TypeUser,
	})
}

// fundGuarantee drives the payment side to the approvable state the way
// production does: the admin creates the funding request (lands
// pending_agent_action), the proof path forwards it (system).
func fundGuarantee(ctx context.Context, t *testing.T, moka *client.MokaClient, adminID, accountID, enrollmentID uuid.UUID) {
	t.Helper()

	octx := opsCtx(ctx, adminID)
	profile, err := moka.Payment.Admin.GetAgentProfile(octx, &paymentv1.GetAgentProfileRequest{
		Selector: &paymentv1.GetAgentProfileRequest_AccountId{AccountId: accountID.String()},
	})
	if err != nil {
		t.Fatalf("resolving agent profile: %v", err)
	}

	refType := paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_ENROLLMENT
	refID := enrollmentID.String()
	req, err := moka.Payment.Ledger.CreateLedgerRequest(octx, &paymentv1.CreateLedgerRequestRequest{
		AgentProfileId:      profile.Id,
		Type:                paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_FUNDING,
		Amount:              2000,
		ReferenceEntityType: &refType,
		ReferenceEntityId:   &refID,
	})
	if err != nil {
		t.Fatalf("creating guarantee request: %v", err)
	}

	sctx := passport.AddToContext(ctx, passport.SystemPassport())
	if _, err := moka.Payment.Ledger.UpdateLedgerRequestStatus(sctx, &paymentv1.UpdateLedgerRequestStatusRequest{
		Id:     req.Id,
		Status: paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_PENDING_ADMIN_APPROVAL,
	}); err != nil {
		t.Fatalf("forwarding guarantee request: %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func testAccount(ctx context.Context, t *testing.T, accounts authrepo.Store, first string) uuid.UUID {
	t.Helper()

	phone := fmt.Sprintf("+2439%09d", time.Now().UnixNano()%1_000_000_000)
	acc, err := accounts.AddAccount(ctx, auth.NewAccount{
		PhoneNumber: phone,
		FirstName:   first,
		LastName:    "Usecase",
	}, "")
	if err != nil {
		t.Fatalf("creating test account: %v", err)
	}
	return acc.ID
}

func role(ctx context.Context, t *testing.T, accounts authrepo.Store, id uuid.UUID) auth.AccountRole {
	t.Helper()
	acc, err := accounts.GetAccount(ctx, id.String())
	if err != nil {
		t.Fatalf("reading account role: %v", err)
	}
	return acc.Role
}

func TestEnrollmentUseCaseFullFlow(t *testing.T) {
	uc, notifier, accounts, moka := newTestUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidate := testAccount(ctx, t, accounts, "Candidate")
	admin := testAccount(ctx, t, accounts, "Admin")

	// Invite: enrollment created, role flipped, candidate notified.
	invited, err := uc.Invite(ctx, candidate, admin)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if got := role(ctx, t, accounts, candidate); got != auth.AccountRolePendingAgent {
		t.Fatalf("role after invite = %q, want pending_agent", got)
	}
	if len(notifier.events) != 1 || notifier.events[0] != "enrollment.invited" {
		t.Errorf("events after invite = %v", notifier.events)
	}

	// A pending_agent is not eligible for a second invite (role guard, not
	// just the unique index).
	if _, err := uc.Invite(ctx, candidate, admin); !errors.Is(err, enrollment.ErrNotEligible) {
		t.Errorf("re-invite error = %v, want ErrNotEligible", err)
	}

	// Accept: stale TOS rejected, current one stamps.
	if _, err := uc.Accept(ctx, candidate, invited.ID, "0.9"); !errors.Is(err, enrollment.ErrTOSVersionMismatch) {
		t.Errorf("stale TOS error = %v, want ErrTOSVersionMismatch", err)
	}
	accepted, err := uc.Accept(ctx, candidate, invited.ID, tosVersion)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.TOSVersion == nil || *accepted.TOSVersion != tosVersion {
		t.Fatalf("accepted = %+v", accepted)
	}

	// Approving before the guarantee is even requested fails cleanly.
	if _, err := uc.Approve(opsCtx(ctx, admin), admin, invited.ID); !errors.Is(err, enrollment.ErrGuaranteeNotSettled) {
		t.Fatalf("approve-without-guarantee error = %v, want ErrGuaranteeNotSettled", err)
	}

	// Fund the guarantee the way production does (admin creates, proof
	// path forwards).
	fundGuarantee(ctx, t, moka, admin, candidate, invited.ID)

	// Approve of a suspended account rolls the whole transition back.
	suspended := auth.AccountStatusSuspended
	if _, err := accounts.UpdateAccount(ctx, auth.UpdateAccount{ID: candidate, Status: new(suspended)}); err != nil {
		t.Fatalf("suspending candidate: %v", err)
	}
	if _, err := uc.Approve(opsCtx(ctx, admin), admin, invited.ID); !errors.Is(err, enrollment.ErrNotEligible) {
		t.Fatalf("approve-suspended error = %v, want ErrNotEligible", err)
	}
	still, err := uc.MyEnrollment(ctx, candidate)
	if err != nil || still.Status != enrollment.StatusAccepted {
		t.Fatalf("enrollment after rolled-back approve = %+v, %v — the approval must not stick", still, err)
	}
	if got := role(ctx, t, accounts, candidate); got != auth.AccountRolePendingAgent {
		t.Fatalf("role after rolled-back approve = %q", got)
	}

	// Reactivate and approve for real: role → agent, second notification.
	active := auth.AccountStatusActive
	if _, err := accounts.UpdateAccount(ctx, auth.UpdateAccount{ID: candidate, Status: new(active)}); err != nil {
		t.Fatalf("reactivating candidate: %v", err)
	}
	approved, err := uc.Approve(opsCtx(ctx, admin), admin, invited.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != enrollment.StatusApproved {
		t.Fatalf("approved = %+v", approved)
	}
	if got := role(ctx, t, accounts, candidate); got != auth.AccountRoleAgent {
		t.Fatalf("role after approve = %q, want agent", got)
	}
	if len(notifier.events) != 2 || notifier.events[1] != "enrollment.approved" {
		t.Errorf("events after approve = %v", notifier.events)
	}
}

func TestEnrollmentUseCaseDeclineAndCancel(t *testing.T) {
	uc, _, accounts, _ := newTestUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin := testAccount(ctx, t, accounts, "Admin")

	// Decline restores the user role.
	decliner := testAccount(ctx, t, accounts, "Decliner")
	inv, err := uc.Invite(ctx, decliner, admin)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.Decline(ctx, decliner, inv.ID); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if got := role(ctx, t, accounts, decliner); got != auth.AccountRoleUser {
		t.Fatalf("role after decline = %q, want user", got)
	}

	// Cancel from accepted restores the user role too.
	inv2, err := uc.Invite(ctx, decliner, admin)
	if err != nil {
		t.Fatalf("re-Invite: %v", err)
	}
	if _, err := uc.Accept(ctx, decliner, inv2.ID, tosVersion); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := uc.Cancel(ctx, admin, inv2.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := role(ctx, t, accounts, decliner); got != auth.AccountRoleUser {
		t.Fatalf("role after cancel = %q, want user", got)
	}

	// An ops account can never be invited.
	opsRole := auth.AccountRoleOps
	ops := testAccount(ctx, t, accounts, "Ops")
	if _, err := accounts.UpdateAccount(ctx, auth.UpdateAccount{ID: ops, Role: new(opsRole)}); err != nil {
		t.Fatalf("promoting ops: %v", err)
	}
	if _, err := uc.Invite(ctx, ops, admin); !errors.Is(err, enrollment.ErrNotEligible) {
		t.Errorf("invite-ops error = %v, want ErrNotEligible", err)
	}
}
