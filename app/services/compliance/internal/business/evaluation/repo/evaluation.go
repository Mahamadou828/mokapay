// Package repo owns the AML persistence: the append-only
// rule_evaluations log, the alerts built on top of it, and the two
// derived reads (account risk, dashboard counts). Every write goes
// through a compliance_ext procedure; this layer only maps outcomes.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	"mokapay.app/app/services/compliance/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction — the rows of
// one evaluation run land together or not at all.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// Add appends one audit row; a triggered one also opens its alert, in the
// same statement.
func (s Store) Add(ctx context.Context, data evaluation.NewRuleEvaluation) (evaluation.RuleEvaluation, error) {
	params := sqlc.AddRuleEvaluationParams{
		TransferNk:  data.TransferID.String(),
		RuleCode:    sqlc.ComplianceRuleCode(data.RuleCode),
		SubjectType: sqlc.ComplianceSubjectType(data.SubjectType),
		Triggered:   data.Triggered,
		Inputs:      data.Inputs,
		AccountNk:   toNullUUID(data.AccountID),
		RiskPoints:  data.RiskPoints,
	}
	if data.RecipientPhone != nil {
		params.RecipientPhone = pgtype.Text{String: *data.RecipientPhone, Valid: true}
	}
	if data.Severity != nil {
		params.Severity = sqlc.NullComplianceSeverity{
			ComplianceSeverity: sqlc.ComplianceSeverity(*data.Severity),
			Valid:              true,
		}
	}

	row, err := s.sqlc.AddRuleEvaluation(ctx, params)
	if err != nil {
		return evaluation.RuleEvaluation{}, fmt.Errorf("adding rule evaluation: %w", err)
	}

	return evaluationToDomain(row)
}

// ListByTransfer returns everything that was checked on one transfer.
func (s Store) ListByTransfer(ctx context.Context, transferID string) ([]evaluation.RuleEvaluation, error) {
	rows, err := s.sqlc.ListRuleEvaluationsByTransfer(ctx, transferID)
	if err != nil {
		return nil, fmt.Errorf("listing rule evaluations of transfer %s: %w", transferID, err)
	}

	out := make([]evaluation.RuleEvaluation, 0, len(rows))
	for _, row := range rows {
		e, err := viewEvaluationToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}

	return out, nil
}

// GetAlert returns the alert with the given id.
func (s Store) GetAlert(ctx context.Context, id string) (evaluation.Alert, error) {
	row, err := s.sqlc.GetAlert(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return evaluation.Alert{}, evaluation.ErrAlertNotFound
		}
		return evaluation.Alert{}, fmt.Errorf("getting alert %s: %w", id, err)
	}

	return alertToDomain(row)
}

// ListAlerts returns the worklist matching the filter, newest first.
func (s Store) ListAlerts(ctx context.Context, f evaluation.AlertFilter, limit int32) ([]evaluation.Alert, error) {
	params := alertFilterToParams(f)
	params.MaxRows = limit

	rows, err := s.sqlc.ListAlerts(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}

	out := make([]evaluation.Alert, 0, len(rows))
	for _, row := range rows {
		a, err := alertToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}

	return out, nil
}

// UpdateAlert applies the review move and returns the alert as the read
// model sees it (the procedure returns the base row, which carries none
// of the joined rule columns — so the updated alert is read back).
// evaluation.ErrAlertNotFound = unknown or soft-deleted id.
func (s Store) UpdateAlert(ctx context.Context, data evaluation.UpdateAlert) (evaluation.Alert, error) {
	params := sqlc.UpdateAlertStatusParams{
		ID:         data.ID.String(),
		Status:     sqlc.ComplianceAlertStatus(data.Status),
		AssigneeNk: toNullUUID(data.AssigneeID),
	}
	if data.ResolutionNote != nil {
		params.ResolutionNote = pgtype.Text{String: *data.ResolutionNote, Valid: true}
	}

	if _, err := s.sqlc.UpdateAlertStatus(ctx, params); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return evaluation.Alert{}, evaluation.ErrAlertNotFound
		}
		return evaluation.Alert{}, fmt.Errorf("updating alert %s: %w", data.ID, err)
	}

	return s.GetAlert(ctx, data.ID.String())
}

// GetAccountRisk returns the account's 90-day score. The Level field is
// left unset on purpose: the score→band bucketing is env config, so the
// usecase applies it. An account with no evaluation in the window scores
// zero rather than "not found" — absence of signal is a real answer.
func (s Store) GetAccountRisk(ctx context.Context, accountID string) (evaluation.AccountRisk, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return evaluation.AccountRisk{}, fmt.Errorf("parsing account id %q: %w", accountID, err)
	}

	row, err := s.sqlc.GetAccountRisk(ctx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return evaluation.AccountRisk{AccountID: id}, nil
		}
		return evaluation.AccountRisk{}, fmt.Errorf("getting account risk %s: %w", accountID, err)
	}

	out := evaluation.AccountRisk{AccountID: id, Score: row.RiskScore}
	if !row.LastEvaluatedAt.IsZero() {
		out.LastEvaluatedAt = new(row.LastEvaluatedAt)
	}

	return out, nil
}

// GetDashboard returns the admin-page counts.
func (s Store) GetDashboard(ctx context.Context) (evaluation.Dashboard, error) {
	row, err := s.sqlc.GetDashboardCounts(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return evaluation.Dashboard{}, nil
		}
		return evaluation.Dashboard{}, fmt.Errorf("getting dashboard counts: %w", err)
	}

	return evaluation.Dashboard{
		OpenAlerts:           row.OpenAlerts,
		UnderReviewAlerts:    row.UnderReviewAlerts,
		EscalatedAlerts:      row.EscalatedAlerts,
		HighSeverityAlerts:   row.HighSeverityAlerts,
		MediumSeverityAlerts: row.MediumSeverityAlerts,
		LowSeverityAlerts:    row.LowSeverityAlerts,
		FlaggedTransfers:     row.FlaggedTransfers,
		OpenCases:            row.OpenCases,
		InvestigatingCases:   row.InvestigatingCases,
		SARCases:             row.SarCases,
	}, nil
}

func evaluationToDomain(r sqlc.RuleEvaluation) (evaluation.RuleEvaluation, error) {
	return buildEvaluation(r.ID, r.TransferNk, r.RuleCode, r.SubjectType, r.AccountNk,
		r.RecipientPhone, r.Triggered, r.Severity, r.RiskPoints, r.Inputs, r.EvaluatedAt)
}

func viewEvaluationToDomain(r sqlc.ComplianceExtRuleEvaluation) (evaluation.RuleEvaluation, error) {
	return buildEvaluation(r.ID, r.TransferNk, r.RuleCode, r.SubjectType, r.AccountNk,
		r.RecipientPhone, r.Triggered, r.Severity, r.RiskPoints, r.Inputs, r.EvaluatedAt)
}

func buildEvaluation(
	id, transferNk string, code sqlc.ComplianceRuleCode, subject sqlc.ComplianceSubjectType,
	accountNk uuid.NullUUID, phone pgtype.Text, triggered bool,
	severity sqlc.NullComplianceSeverity, points int32, inputs []byte, evaluatedAt time.Time,
) (evaluation.RuleEvaluation, error) {
	eid, err := uuid.Parse(id)
	if err != nil {
		return evaluation.RuleEvaluation{}, fmt.Errorf("parsing rule evaluation id %q: %w", id, err)
	}
	tid, err := uuid.Parse(transferNk)
	if err != nil {
		return evaluation.RuleEvaluation{}, fmt.Errorf("parsing transfer id %q: %w", transferNk, err)
	}

	out := evaluation.RuleEvaluation{
		ID:          eid,
		TransferID:  tid,
		RuleCode:    evaluation.RuleCode(code),
		SubjectType: evaluation.SubjectType(subject),
		Triggered:   triggered,
		RiskPoints:  points,
		Inputs:      inputs,
		EvaluatedAt: evaluatedAt,
	}
	if accountNk.Valid {
		out.AccountID = new(accountNk.UUID)
	}
	if phone.Valid {
		out.RecipientPhone = new(phone.String)
	}
	if severity.Valid {
		out.Severity = new(evaluation.Severity(severity.ComplianceSeverity))
	}

	return out, nil
}

func alertToDomain(r sqlc.ComplianceExtAlert) (evaluation.Alert, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return evaluation.Alert{}, fmt.Errorf("parsing alert id %q: %w", r.ID, err)
	}
	evalID, err := uuid.Parse(r.RuleEvaluationFk)
	if err != nil {
		return evaluation.Alert{}, fmt.Errorf("parsing rule evaluation id %q: %w", r.RuleEvaluationFk, err)
	}
	transferID, err := uuid.Parse(r.TransferNk)
	if err != nil {
		return evaluation.Alert{}, fmt.Errorf("parsing transfer id %q: %w", r.TransferNk, err)
	}

	out := evaluation.Alert{
		ID:               id,
		RuleEvaluationID: evalID,
		Status:           evaluation.AlertStatus(r.Status),
		RuleCode:         evaluation.RuleCode(r.RuleCode),
		SubjectType:      evaluation.SubjectType(r.SubjectType),
		TransferID:       transferID,
		RiskPoints:       r.RiskPoints,
		EvaluatedAt:      r.EvaluatedAt,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
	if r.AssigneeNk.Valid {
		out.AssigneeID = new(r.AssigneeNk.UUID)
	}
	if r.ResolutionNote.Valid {
		out.ResolutionNote = new(r.ResolutionNote.String)
	}
	if r.Severity.Valid {
		out.Severity = new(evaluation.Severity(r.Severity.ComplianceSeverity))
	}
	if r.AccountNk.Valid {
		out.AccountID = new(r.AccountNk.UUID)
	}
	if r.RecipientPhone.Valid {
		out.RecipientPhone = new(r.RecipientPhone.String)
	}

	return out, nil
}

// alertFilterToParams maps the optional filters to the sqlc null types —
// nil means "not filtering".
func alertFilterToParams(f evaluation.AlertFilter) sqlc.ListAlertsParams {
	var p sqlc.ListAlertsParams
	if f.Status != nil {
		p.Status = sqlc.NullComplianceAlertStatus{
			ComplianceAlertStatus: sqlc.ComplianceAlertStatus(*f.Status),
			Valid:                 true,
		}
	}
	if f.Severity != nil {
		p.Severity = sqlc.NullComplianceSeverity{
			ComplianceSeverity: sqlc.ComplianceSeverity(*f.Severity),
			Valid:              true,
		}
	}
	if f.AccountID != nil {
		p.AccountNk = uuid.NullUUID{UUID: *f.AccountID, Valid: true}
	}
	return p
}

func toNullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}
