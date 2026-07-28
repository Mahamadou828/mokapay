package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrAccountNotFound = errors.New("account not found")

// ErrPhoneNumberTaken is returned by AddAccount when a live account already
// owns the phone (the partial unique index on accounts.phone_number).
var ErrPhoneNumberTaken = errors.New("phone number already registered")

// ErrCognitoSubTaken is returned by AddAccount when a live account is already
// bound to the Cognito user (the partial unique index on accounts.cognito_sub).
var ErrCognitoSubTaken = errors.New("cognito user already bound to an account")

// ErrAccountSuspended / ErrAccountBanned block authentication (login, token
// refresh) for moderated accounts.
var ErrAccountSuspended = errors.New("account suspended")
var ErrAccountBanned = errors.New("account banned")

// ErrRoleNotAllowed rejects a staff creation whose requested role is not
// a back-office one (ops/admin) — customer-facing roles go through the
// register and enrollment flows, never through provisioning.
var ErrRoleNotAllowed = errors.New("role not allowed for staff creation")

// AccountRole values mirror the stored account_role enum.
type AccountRole string

const (
	AccountRoleUser         AccountRole = "user"
	AccountRolePendingAgent AccountRole = "pending_agent"
	AccountRoleAgent        AccountRole = "agent"
	AccountRoleOps          AccountRole = "ops"
	AccountRoleAdmin        AccountRole = "admin"
)

// AccountStatus values mirror the stored account_status enum.
type AccountStatus string

const (
	AccountStatusPending   AccountStatus = "pending"
	AccountStatusActive    AccountStatus = "active"
	AccountStatusSuspended AccountStatus = "suspended"
	AccountStatusBanned    AccountStatus = "banned"
)

// Language values mirror the stored account_language enum.
type Language string

const (
	LanguageFr Language = "fr"
	LanguageEn Language = "en"
)

// NewAccount is the register payload. Code and Password are auth-flow fields
// (OTP verify, Cognito sign-up) — the repo never persists them. CognitoSub is
// set by the usecase after provider sign-up, before the row is stored.
type NewAccount struct {
	PhoneNumber       string
	Code              string
	Password          string
	FirstName         string
	LastName          string
	PreferredLanguage Language // zero value = store default (fr)
	Email             *string
}

// UpdateAccount is the account update payload; nil fields are left unchanged.
// Role and Status are moderation fields — the api layer only sets them on the
// admin path (authorization is its job).
type UpdateAccount struct {
	ID                uuid.UUID
	FirstName         *string
	LastName          *string
	Email             *string
	PreferredLanguage *Language
	Role              *AccountRole
	Status            *AccountStatus
}

// AccountFilter narrows account listings; nil fields mean "not filtering".
type AccountFilter struct {
	Role        *AccountRole
	Status      *AccountStatus
	PhoneNumber *string
}

// Account is the read entity. Credentials live in Cognito, never here.
type Account struct {
	ID                uuid.UUID
	PhoneNumber       string
	CognitoSub        *string // nil until the Cognito user is provisioned
	FirstName         string
	LastName          string
	Email             *string
	PreferredLanguage Language
	Role              AccountRole
	Status            AccountStatus
	LastLoginAt       *time.Time // nil until the first login
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
