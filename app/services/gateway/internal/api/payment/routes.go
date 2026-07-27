package payment

import (
	"net/http"

	core "mokapay.app/core/http"
)

// Routes mounts the payment-service surface on the route tiers: authed
// (any signed-in client — the transfer flow), agent (role-gated inbox,
// wallets, assignments) and ops (worklist + escape hatch).
func (h Handler) Routes(authed, agent, ops *core.Group) {
	// The client transfer flow. /transfers/operators and /transfers/quote
	// are exact patterns — the stdlib mux prefers them over
	// /transfers/{id}.
	authed.RegisterHandler(http.MethodGet, "/transfers/operators", h.getAvailableOperators)
	authed.RegisterHandler(http.MethodPost, "/transfers/quote", h.getQuote)
	authed.RegisterHandler(http.MethodPost, "/transfers", h.createTransfer)
	authed.RegisterHandler(http.MethodGet, "/transfers", h.listTransfers)
	authed.RegisterHandler(http.MethodGet, "/transfers/{id}", h.getTransfer)
	authed.RegisterHandler(http.MethodGet, "/transfers/{id}/events", h.listTransferEvents)
	authed.RegisterHandler(http.MethodPost, "/transfers/{id}/confirm", h.confirmReceipt)
	authed.RegisterHandler(http.MethodPost, "/transfers/{id}/cancel", h.cancelTransfer)
	authed.RegisterHandler(http.MethodGet, "/beneficiaries", h.listBeneficiaries)
	authed.RegisterHandler(http.MethodDelete, "/beneficiaries/{id}", h.deleteBeneficiary)

	// The proof upload flow (presign + confirm): the bytes go straight to
	// object storage; the file service enforces owner scoping.
	authed.RegisterHandler(http.MethodPost, "/files", h.createUpload)
	authed.RegisterHandler(http.MethodPost, "/files/{id}/confirm", h.confirmUpload)
	authed.RegisterHandler(http.MethodGet, "/files/{id}", h.getFile)
	authed.RegisterHandler(http.MethodGet, "/files", h.listFiles)

	// The agent surface.
	agent.RegisterHandler(http.MethodGet, "/agent/profile", h.getMyProfile)
	agent.RegisterHandler(http.MethodGet, "/agent/deposit-channels", h.listDepositChannels)
	agent.RegisterHandler(http.MethodGet, "/agent/ledger/requests", h.listMyLedgerRequests)
	agent.RegisterHandler(http.MethodPost, "/agent/ledger/requests", h.createMyLedgerRequest)
	agent.RegisterHandler(http.MethodGet, "/agent/ledger/lines", h.listMyLedgerLines)
	agent.RegisterHandler(http.MethodGet, "/agent/offers", h.listMyOffers)
	agent.RegisterHandler(http.MethodPost, "/agent/offers/{id}/accept", h.acceptOffer)
	agent.RegisterHandler(http.MethodPost, "/agent/offers/{id}/decline", h.declineOffer)
	agent.RegisterHandler(http.MethodGet, "/agent/transfers", h.listMyTransfers)
	agent.RegisterHandler(http.MethodGet, "/agent/wallets", h.listMyWallets)
	agent.RegisterHandler(http.MethodPost, "/agent/wallets", h.addWallet)
	agent.RegisterHandler(http.MethodDelete, "/agent/wallets/{id}", h.removeWallet)

	// The ops surface.
	ops.RegisterHandler(http.MethodGet, "/admin/transfers", h.listAllTransfers)
	ops.RegisterHandler(http.MethodPost, "/admin/transfers/{id}/cancel", h.cancelTransferAdmin)
	ops.RegisterHandler(http.MethodGet, "/admin/ledger/requests", h.listLedgerRequestsAdmin)
	ops.RegisterHandler(http.MethodPost, "/admin/ledger/requests", h.createLedgerRequestAdmin)
	ops.RegisterHandler(http.MethodGet, "/admin/ledger/requests/{id}", h.getLedgerRequest)
	ops.RegisterHandler(http.MethodPost, "/admin/ledger/requests/{id}/approve", h.approveLedgerRequest)
	ops.RegisterHandler(http.MethodPost, "/admin/ledger/requests/{id}/reject", h.rejectLedgerRequest)
	ops.RegisterHandler(http.MethodGet, "/admin/ledger/lines", h.listLedgerLinesAdmin)
	ops.RegisterHandler(http.MethodGet, "/admin/agent-profiles", h.getAgentProfileByAccount)
	ops.RegisterHandler(http.MethodGet, "/admin/agent-profiles/{id}", h.getAgentProfileAdmin)
	ops.RegisterHandler(http.MethodPost, "/admin/agent-profiles/{id}/activate", h.activateAgentProfile)
	ops.RegisterHandler(http.MethodPost, "/admin/agent-profiles/{id}/suspend", h.suspendAgentProfile)
	ops.RegisterHandler(http.MethodPost, "/admin/files/{id}/reject", h.rejectFile)
}
