package user

import (
	"net/http"

	core "mokapay.app/core/http"
)

// Routes mounts the user-service surface on the three route tiers: public
// (anonymous auth flows), authed (valid token required) and ops (token +
// ops/admin role).
func (h Handler) Routes(public, authed, ops *core.Group) {
	public.RegisterHandler(http.MethodPost, "/auth/otp", h.sendOtp)
	public.RegisterHandler(http.MethodPost, "/auth/register", h.register)
	public.RegisterHandler(http.MethodPost, "/auth/login", h.login)
	public.RegisterHandler(http.MethodPost, "/auth/refresh", h.refreshToken)
	public.RegisterHandler(http.MethodPost, "/auth/password", h.changePassword)

	authed.RegisterHandler(http.MethodPost, "/auth/logout", h.logout)
	authed.RegisterHandler(http.MethodGet, "/me", h.me)
	authed.RegisterHandler(http.MethodPatch, "/me", h.updateMe)

	ops.RegisterHandler(http.MethodGet, "/accounts", h.listAccounts)
	ops.RegisterHandler(http.MethodGet, "/accounts/{id}", h.getAccount)
	ops.RegisterHandler(http.MethodPatch, "/accounts/{id}", h.updateAccount)
	ops.RegisterHandler(http.MethodDelete, "/accounts/{id}", h.deleteAccount)
}
