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

var ErrBeneficiaryNotFound = errors.New("beneficiary not found")

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
