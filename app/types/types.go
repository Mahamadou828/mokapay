// Package types - define application level types that are shared accross multiple services for easier implementation
package types

// Entity - represent all the different entities in our system
type Entity string

const (
	EntityLedgerRequest Entity = "ledger_request"
	EntityTransfer      Entity = "transfer"
	EntityUser          Entity = "user"
	EntityFile          Entity = "file"
)
