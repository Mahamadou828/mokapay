package dispute_test

import (
	"errors"
	"testing"

	"mokapay.app/app/services/payment/internal/business/dispute"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// TestResolutionTable is the spec's "Admin resolution outcomes" table,
// verbatim. Every row fixes three things at once — what happens to the
// hold, whether the client is refunded, and where the transfer lands —
// and the procedures apply exactly what this returns, so getting a row
// wrong here is money moving the wrong way.
func TestResolutionTable(t *testing.T) {
	tests := []struct {
		name       string
		resolution dispute.Resolution
		want       dispute.Effect
	}{
		{
			name:       "agent proven right: the payout stands",
			resolution: dispute.ResolutionAgentRight,
			want: dispute.Effect{
				HoldOutcome:    dispute.HoldReleased,
				TerminalStatus: transfer.StatusDone,
				RefundClient:   false,
			},
		},
		{
			name:       "agent at fault: capture funds the client's refund",
			resolution: dispute.ResolutionAgentFault,
			want: dispute.Effect{
				HoldOutcome:    dispute.HoldCaptured,
				TerminalStatus: transfer.StatusRefunded,
				RefundClient:   true,
			},
		},
		{
			name:       "client fraud: make the agent whole, no refund",
			resolution: dispute.ResolutionClientFraud,
			want: dispute.Effect{
				HoldOutcome:    dispute.HoldReleased,
				TerminalStatus: transfer.StatusDone,
				RefundClient:   false,
			},
		},
		{
			name:       "no fault: unwind, nobody was out money",
			resolution: dispute.ResolutionNoFault,
			want: dispute.Effect{
				HoldOutcome:    dispute.HoldReleased,
				TerminalStatus: transfer.StatusCancelled,
				RefundClient:   false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dispute.EffectOf(tt.resolution)
			if err != nil {
				t.Fatalf("EffectOf(%s): %v", tt.resolution, err)
			}
			if got != tt.want {
				t.Fatalf("EffectOf(%s) = %+v, want %+v", tt.resolution, got, tt.want)
			}
		})
	}
}

func TestEffectOfRejectsUnknownResolution(t *testing.T) {
	// The zero value is what an UNSPECIFIED proto enum maps to: it must
	// never fall through to a default that moves money.
	if _, err := dispute.EffectOf(""); !errors.Is(err, dispute.ErrInvalidResolution) {
		t.Fatalf("EffectOf(\"\") = %v, want ErrInvalidResolution", err)
	}
	if _, err := dispute.EffectOf("agent_mostly_right"); !errors.Is(err, dispute.ErrInvalidResolution) {
		t.Fatalf("EffectOf(unknown) = %v, want ErrInvalidResolution", err)
	}
}

// TestCompletes: a ruling that leaves the payout standing completes the
// transfer, which both earns the agent their share (being defrauded by the
// client does not cost them the work they did) and sends the movement for
// AML evaluation. A refund or a cancellation does neither.
func TestCompletes(t *testing.T) {
	tests := map[dispute.Resolution]bool{
		dispute.ResolutionAgentRight:  true,
		dispute.ResolutionClientFraud: true,
		dispute.ResolutionAgentFault:  false,
		dispute.ResolutionNoFault:     false,
	}

	for resolution, want := range tests {
		effect, err := dispute.EffectOf(resolution)
		if err != nil {
			t.Fatalf("EffectOf(%s): %v", resolution, err)
		}
		if got := effect.Completes(); got != want {
			t.Errorf("%s completes the transfer = %v, want %v", resolution, got, want)
		}
	}
}

// TestCaptureImpliesRefund: the capture exists to fund the refund. The
// two must never come apart — a capture with nobody to pay would just be
// taking an agent's money.
func TestCaptureImpliesRefund(t *testing.T) {
	for _, r := range []dispute.Resolution{
		dispute.ResolutionAgentRight, dispute.ResolutionAgentFault,
		dispute.ResolutionClientFraud, dispute.ResolutionNoFault,
	} {
		effect, err := dispute.EffectOf(r)
		if err != nil {
			t.Fatalf("EffectOf(%s): %v", r, err)
		}
		if (effect.HoldOutcome == dispute.HoldCaptured) != effect.RefundClient {
			t.Errorf("%s: capture=%v but refund=%v — they must move together",
				r, effect.HoldOutcome == dispute.HoldCaptured, effect.RefundClient)
		}
	}
}
