package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	"mokapay.app/app/services/compliance/internal/business/evaluation/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated compliance database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/...
func newTestStore(t *testing.T) repo.Store {
	t.Helper()

	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		t.Skip("TEST_DB_PORT not set; integration test needs a migrated compliance DB")
	}

	db := coredb.New(coredb.PostgresConfig{
		Host:     getenv("TEST_DB_HOST", "localhost"),
		Port:     port,
		Username: getenv("TEST_DB_USER", "mokapay"),
		Password: getenv("TEST_DB_PASSWORD", "mokapay"),
		Database: getenv("TEST_DB_NAME", "compliance"),
	})
	if err := db.Open(); err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return repo.New(db)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// senderRow builds a sender-subject audit row for one account.
func senderRow(transferID, accountID uuid.UUID, code evaluation.RuleCode, triggered bool, severity evaluation.Severity, points int32) evaluation.NewRuleEvaluation {
	row := evaluation.NewRuleEvaluation{
		TransferID:  transferID,
		RuleCode:    code,
		SubjectType: evaluation.SubjectSender,
		AccountID:   new(accountID),
		Triggered:   triggered,
		Inputs:      json.RawMessage(`{"rolling_24h_volume_usd":50000,"daily_limit_usd":10000}`),
	}
	if triggered {
		row.Severity = new(severity)
		row.RiskPoints = points
	}
	return row
}

// A triggered evaluation and the alert that justifies it are one fact:
// the procedure writes both, so the audit log can never hold a triggered
// row nobody was ever asked to review.
func TestAddTriggeredOpensAnAlert(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	transferID, accountID := uuid.New(), uuid.New()
	saved, err := store.Add(ctx, senderRow(transferID, accountID,
		evaluation.RuleDailyVolumeLimit, true, evaluation.SeverityHigh, 35))
	if err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}

	if !saved.Triggered {
		t.Fatal("saved row is not marked triggered")
	}
	if saved.Severity == nil || *saved.Severity != evaluation.SeverityHigh {
		t.Fatalf("severity = %v, want high", saved.Severity)
	}
	if saved.RiskPoints != 35 {
		t.Fatalf("risk points = %d, want 35", saved.RiskPoints)
	}
	if saved.AccountID == nil || *saved.AccountID != accountID {
		t.Fatalf("account id = %v, want %s", saved.AccountID, accountID)
	}

	alerts, err := store.ListAlerts(ctx, evaluation.AlertFilter{AccountID: new(accountID)}, 10)
	if err != nil {
		t.Fatalf("listing alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want exactly 1", len(alerts))
	}
	a := alerts[0]
	if a.RuleEvaluationID != saved.ID {
		t.Fatalf("alert points at evaluation %s, want %s", a.RuleEvaluationID, saved.ID)
	}
	if a.Status != evaluation.AlertOpen {
		t.Fatalf("status = %q, want open", a.Status)
	}
	if a.TransferID != transferID {
		t.Fatalf("alert transfer = %s, want %s", a.TransferID, transferID)
	}
	if a.RuleCode != evaluation.RuleDailyVolumeLimit {
		t.Fatalf("alert rule = %q, want daily_volume_limit", a.RuleCode)
	}
}

// A rule that did not fire is still recorded — that is the point of the
// trail — but it must never raise an alert.
func TestAddQuietRowRaisesNoAlert(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	transferID, accountID := uuid.New(), uuid.New()
	if _, err := store.Add(ctx, senderRow(transferID, accountID,
		evaluation.RuleHourlyVelocity, false, "", 0)); err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}

	alerts, err := store.ListAlerts(ctx, evaluation.AlertFilter{AccountID: new(accountID)}, 10)
	if err != nil {
		t.Fatalf("listing alerts: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("got %d alerts on a non-triggered evaluation, want 0", len(alerts))
	}

	rows, err := store.ListByTransfer(ctx, transferID.String())
	if err != nil {
		t.Fatalf("listing evaluations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d audit rows, want 1", len(rows))
	}
	if rows[0].Triggered || rows[0].Severity != nil || rows[0].RiskPoints != 0 {
		t.Fatalf("quiet row carries trigger state: %+v", rows[0])
	}
}

// A recipient subject is identified by phone and carries no account — the
// table's CHECK constraint enforces the same pairing.
func TestAddRecipientSubject(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	transferID := uuid.New()
	phone := "+2438" + uuid.NewString()[:8]
	saved, err := store.Add(ctx, evaluation.NewRuleEvaluation{
		TransferID:     transferID,
		RuleCode:       evaluation.RuleBeneficiaryConcentration,
		SubjectType:    evaluation.SubjectRecipient,
		RecipientPhone: new(phone),
		Triggered:      true,
		Severity:       new(evaluation.SeverityHigh),
		RiskPoints:     35,
		Inputs:         json.RawMessage(`{"distinct_senders_7d":12}`),
	})
	if err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}

	if saved.AccountID != nil {
		t.Fatalf("recipient row carries an account id: %v", saved.AccountID)
	}
	if saved.RecipientPhone == nil || *saved.RecipientPhone != phone {
		t.Fatalf("recipient phone = %v, want %s", saved.RecipientPhone, phone)
	}
}

func TestUpdateAlertStatus(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	accountID := uuid.New()
	if _, err := store.Add(ctx, senderRow(uuid.New(), accountID,
		evaluation.RuleNewAccountHighVolume, true, evaluation.SeverityHigh, 35)); err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}
	alerts, err := store.ListAlerts(ctx, evaluation.AlertFilter{AccountID: new(accountID)}, 10)
	if err != nil || len(alerts) != 1 {
		t.Fatalf("listing alerts: %v (got %d)", err, len(alerts))
	}

	assignee := uuid.New()
	note := "reviewed with the sender, salary transfer"
	updated, err := store.UpdateAlert(ctx, evaluation.UpdateAlert{
		ID:             alerts[0].ID,
		Status:         evaluation.AlertResolved,
		AssigneeID:     new(assignee),
		ResolutionNote: new(note),
	})
	if err != nil {
		t.Fatalf("updating alert: %v", err)
	}

	if updated.Status != evaluation.AlertResolved {
		t.Fatalf("status = %q, want resolved", updated.Status)
	}
	if updated.AssigneeID == nil || *updated.AssigneeID != assignee {
		t.Fatalf("assignee = %v, want %s", updated.AssigneeID, assignee)
	}
	if updated.ResolutionNote == nil || *updated.ResolutionNote != note {
		t.Fatalf("resolution note = %v, want %q", updated.ResolutionNote, note)
	}
	// The joined rule columns survive the read-back.
	if updated.RuleCode != evaluation.RuleNewAccountHighVolume {
		t.Fatalf("rule code = %q, want new_account_high_volume", updated.RuleCode)
	}
}

func TestUpdateUnknownAlert(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.UpdateAlert(ctx, evaluation.UpdateAlert{
		ID:     uuid.New(),
		Status: evaluation.AlertUnderReview,
	})
	if !errors.Is(err, evaluation.ErrAlertNotFound) {
		t.Fatalf("err = %v, want ErrAlertNotFound", err)
	}
}

func TestGetAlertUnknownID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.GetAlert(ctx, uuid.NewString()); !errors.Is(err, evaluation.ErrAlertNotFound) {
		t.Fatalf("err = %v, want ErrAlertNotFound", err)
	}
}

// The score is a SUM over the account's evaluations, computed on read —
// so adding evaluations moves it without anything writing a total.
func TestGetAccountRisk(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	accountID := uuid.New()

	// An account nobody has ever evaluated scores zero — absence of
	// signal is an answer, not a missing row.
	risk, err := store.GetAccountRisk(ctx, accountID.String())
	if err != nil {
		t.Fatalf("getting account risk: %v", err)
	}
	if risk.Score != 0 || risk.LastEvaluatedAt != nil {
		t.Fatalf("unevaluated account: score %d, last %v, want 0 and nil", risk.Score, risk.LastEvaluatedAt)
	}

	if _, err := store.Add(ctx, senderRow(uuid.New(), accountID,
		evaluation.RuleDailyVolumeLimit, true, evaluation.SeverityHigh, 35)); err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}
	if _, err := store.Add(ctx, senderRow(uuid.New(), accountID,
		evaluation.RuleHourlyVelocity, true, evaluation.SeverityMedium, 15)); err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}
	// A quiet row contributes nothing.
	if _, err := store.Add(ctx, senderRow(uuid.New(), accountID,
		evaluation.RuleNightTimeStructuring, false, "", 0)); err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}

	risk, err = store.GetAccountRisk(ctx, accountID.String())
	if err != nil {
		t.Fatalf("getting account risk: %v", err)
	}
	if risk.Score != 50 {
		t.Fatalf("score = %d, want 50", risk.Score)
	}
	if risk.LastEvaluatedAt == nil {
		t.Fatal("last evaluated at is nil after three evaluations")
	}
	// The band is the usecase's call, never the store's.
	if risk.Level != "" {
		t.Fatalf("store set a risk level (%q); that is the usecase's job", risk.Level)
	}
}

// The dashboard is a global count, so assert on the delta this test
// causes rather than on absolute numbers.
func TestGetDashboardCountsOpenAlerts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	before, err := store.GetDashboard(ctx)
	if err != nil {
		t.Fatalf("getting dashboard: %v", err)
	}

	if _, err := store.Add(ctx, senderRow(uuid.New(), uuid.New(),
		evaluation.RuleDailyVolumeLimit, true, evaluation.SeverityHigh, 35)); err != nil {
		t.Fatalf("adding rule evaluation: %v", err)
	}

	after, err := store.GetDashboard(ctx)
	if err != nil {
		t.Fatalf("getting dashboard: %v", err)
	}

	if after.OpenAlerts != before.OpenAlerts+1 {
		t.Fatalf("open alerts %d → %d, want +1", before.OpenAlerts, after.OpenAlerts)
	}
	if after.HighSeverityAlerts != before.HighSeverityAlerts+1 {
		t.Fatalf("high severity alerts %d → %d, want +1", before.HighSeverityAlerts, after.HighSeverityAlerts)
	}
	if after.FlaggedTransfers != before.FlaggedTransfers+1 {
		t.Fatalf("flagged transfers %d → %d, want +1", before.FlaggedTransfers, after.FlaggedTransfers)
	}
}
