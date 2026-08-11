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
	public.RegisterHandler(http.MethodPost, "/auth/otp/validate", h.validateOtp)
	public.RegisterHandler(http.MethodPost, "/auth/register", h.register)
	public.RegisterHandler(http.MethodPost, "/auth/login", h.login)
	public.RegisterHandler(http.MethodPost, "/auth/refresh", h.refreshToken)
	public.RegisterHandler(http.MethodPost, "/auth/password", h.changePassword)

	public.RegisterHandler(http.MethodGet, "/push/vapid-key", h.getVapidPublicKey)

	authed.RegisterHandler(http.MethodPost, "/auth/logout", h.logout)
	authed.RegisterHandler(http.MethodGet, "/me", h.me)
	authed.RegisterHandler(http.MethodPatch, "/me", h.updateMe)
	authed.RegisterHandler(http.MethodGet, "/notifications", h.listNotifications)
	authed.RegisterHandler(http.MethodPost, "/notifications/{id}/read", h.markNotificationRead)
	authed.RegisterHandler(http.MethodPost, "/notifications/read-all", h.markAllNotificationsRead)
	authed.RegisterHandler(http.MethodPost, "/push/subscriptions", h.subscribePush)
	authed.RegisterHandler(http.MethodDelete, "/push/subscriptions", h.unsubscribePush)
	authed.RegisterHandler(http.MethodGet, "/enrollment", h.getMyEnrollment)
	authed.RegisterHandler(http.MethodPost, "/enrollment/self", h.selfEnroll)
	authed.RegisterHandler(http.MethodPost, "/enrollment/accept", h.acceptEnrollment)
	authed.RegisterHandler(http.MethodPost, "/enrollment/decline", h.declineEnrollment)

	ops.RegisterHandler(http.MethodGet, "/accounts", h.listAccounts)
	ops.RegisterHandler(http.MethodPost, "/accounts", h.createAccount)
	ops.RegisterHandler(http.MethodGet, "/accounts/{id}", h.getAccount)
	ops.RegisterHandler(http.MethodPatch, "/accounts/{id}", h.updateAccount)
	ops.RegisterHandler(http.MethodDelete, "/accounts/{id}", h.deleteAccount)
	ops.RegisterHandler(http.MethodPost, "/accounts/{id}/promote", h.promoteUserToAgent)
	ops.RegisterHandler(http.MethodGet, "/enrollments", h.listEnrollments)
	ops.RegisterHandler(http.MethodPost, "/enrollments/{id}/approve", h.approveEnrollment)
	ops.RegisterHandler(http.MethodPost, "/enrollments/{id}/cancel", h.cancelEnrollment)
}
