package compliance

import (
	"net/http"

	core "mokapay.app/core/http"
)

// Routes mounts the compliance staff surface. Everything sits on the ops
// tier: AML alerts and cases are never client- or agent-visible.
func (h Handler) Routes(ops *core.Group) {
	// The alert worklist. /alerts/{id}/status is a distinct pattern from
	// /alerts/{id} — the stdlib mux prefers the more specific one.
	ops.RegisterHandler(http.MethodGet, "/admin/compliance/dashboard", h.getDashboard)
	ops.RegisterHandler(http.MethodGet, "/admin/compliance/alerts", h.listAlerts)
	ops.RegisterHandler(http.MethodGet, "/admin/compliance/alerts/{id}", h.getAlert)
	ops.RegisterHandler(http.MethodPost, "/admin/compliance/alerts/{id}/status", h.updateAlertStatus)

	// Case management.
	ops.RegisterHandler(http.MethodGet, "/admin/compliance/cases", h.listCases)
	ops.RegisterHandler(http.MethodPost, "/admin/compliance/cases", h.createCase)
	ops.RegisterHandler(http.MethodPatch, "/admin/compliance/cases/{id}", h.updateCase)
	ops.RegisterHandler(http.MethodPost, "/admin/compliance/cases/{id}/alerts", h.addAlertToCase)

	// The account risk read.
	ops.RegisterHandler(http.MethodGet, "/admin/compliance/accounts/{id}/risk", h.getAccountRisk)
}
