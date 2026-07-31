package ws

import (
	"context"
	"encoding/json"
	"fmt"
)

// Channel is the one redis pub/sub channel every live event travels on:
// services publish Envelopes to it, every gateway replica subscribes and
// pushes to its own connected clients. A single channel with the addressee
// inside the envelope keeps the wiring flat — no per-account channel
// bookkeeping, and redis does the replica fan-out for free.
const Channel = "live:events"

// Event is what a client receives over its socket. Type reuses the
// notification domain's event_type vocabulary ("transfer.offer_received",
// "transfer.matched", …) so a frontend keys off the same strings for a push
// notification and for a live event; Data carries only what a refetch needs
// (ids, references), never the entity itself.
type Event struct {
	Type string            `json:"event_type"`
	Data map[string]string `json:"data,omitempty"`
}

// Envelope is the bus format: an Event plus its addressee. The embedded
// Event flattens into the same JSON object, so the wire shape stays
// {"account_id": …, "event_type": …, "data": {…}}.
type Envelope struct {
	AccountID string `json:"account_id"`
	Event
}

// Bus is the publish half of the shared message bus — core/cache's redis
// client satisfies it. An interface, so publishers (and the hub) never
// import redis.
type Bus interface {
	Publish(ctx context.Context, channel, payload string) error
}

// Publisher is the service-side sink: a service holds one and publishes at
// the same call site that already notifies the account, so the live event
// and the notification carry the same fact. A Publisher with no bus
// discards silently — live push is a best-effort second sink, never a
// reason to fail the operation that produced the event.
type Publisher struct {
	bus Bus
}

// NewPublisher wires a publisher onto the bus; a nil bus (redis not
// reachable at startup) yields a publisher that drops every event.
func NewPublisher(bus Bus) Publisher {
	return Publisher{bus: bus}
}

// Publish addresses the event to one account and hands it to the bus.
func (p Publisher) Publish(ctx context.Context, accountID string, e Event) error {
	if p.bus == nil {
		return nil
	}

	payload, err := json.Marshal(Envelope{AccountID: accountID, Event: e})
	if err != nil {
		return fmt.Errorf("encoding %s envelope: %w", e.Type, err)
	}

	if err := p.bus.Publish(ctx, Channel, string(payload)); err != nil {
		return fmt.Errorf("publishing %s: %w", e.Type, err)
	}

	return nil
}
