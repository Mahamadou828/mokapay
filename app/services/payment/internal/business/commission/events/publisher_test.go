package events_test

import (
	"testing"
	"time"

	"mokapay.app/app/services/payment/internal/business/commission/events"
)

// TestWeeklyScheduleNext: the payout fires on Tuesdays at the configured
// hour and never twice for the same slot (River asks for the next time
// strictly after "now").
func TestWeeklyScheduleNext(t *testing.T) {
	s := events.WeeklySchedule{Weekday: time.Tuesday, Hour: 6}

	tests := []struct {
		name    string
		current time.Time
		want    time.Time
	}{
		{
			name:    "monday runs the next morning",
			current: time.Date(2026, 7, 27, 23, 0, 0, 0, time.UTC),
			want:    time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC),
		},
		{
			name:    "tuesday before the hour runs today",
			current: time.Date(2026, 7, 28, 5, 59, 0, 0, time.UTC),
			want:    time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC),
		},
		{
			name:    "exactly at the slot moves to next week (never the same slot twice)",
			current: time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC),
			want:    time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC),
		},
		{
			name:    "tuesday after the hour waits a week",
			current: time.Date(2026, 7, 28, 6, 30, 0, 0, time.UTC),
			want:    time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC),
		},
		{
			name:    "wednesday waits for the next tuesday",
			current: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			want:    time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.Next(tt.current); !got.Equal(tt.want) {
				t.Fatalf("Next(%s) = %s, want %s", tt.current, got, tt.want)
			}
		})
	}
}
