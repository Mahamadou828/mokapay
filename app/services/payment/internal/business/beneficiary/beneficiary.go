// Package beneficiary holds the saved-recipient domain types (D7). The
// transfer stores its own recipient snapshot — editing a beneficiary
// never rewrites transfer history.
package beneficiary

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

var (
	ErrBeneficiaryNotFound = errors.New("beneficiary not found")
	// ErrBeneficiaryExists guards the live (client, phone) uniqueness on
	// update — the add path upserts instead, so it never hits this.
	ErrBeneficiaryExists = errors.New("another saved recipient already uses this phone")
)

// Beneficiary is the read entity.
type Beneficiary struct {
	ID        uuid.UUID
	ClientID  uuid.UUID
	Name      string
	Phone     string
	Operator  transfer.Operator
	City      *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewBeneficiary is the create payload; the owning client is an explicit
// repo parameter (passport identity never rides inside a payload).
// Adding an existing live (client, phone) pair refreshes it instead of
// erroring — CreateTransfer with inline recipient details is idempotent
// on the beneficiary.
type NewBeneficiary struct {
	Name     string
	Phone    string
	Operator transfer.Operator
	City     *string
}

// UpdateBeneficiary is the partial-update payload; nil means "don't
// change". The target id and owning client are explicit parameters.
type UpdateBeneficiary struct {
	Name     *string
	Phone    *string
	Operator *transfer.Operator
	City     *string
}
