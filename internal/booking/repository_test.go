package booking

import (
	"testing"
	"time"
)

func TestHotelLocalToday(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		timezone string
		want     time.Time
	}{
		{
			name:     "India local date",
			now:      time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC),
			timezone: "Asia/Kolkata",
			want:     time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "Zurich local date",
			now:      time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC),
			timezone: "Europe/Zurich",
			want:     time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "New York is still on previous day",
			now:      time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC),
			timezone: "America/New_York",
			want:     time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hotelLocalToday(tt.now, tt.timezone)
			if err != nil {
				t.Fatalf("hotelLocalToday() error = %v", err)
			}

			if !got.Equal(tt.want) {
				t.Errorf(
					"hotelLocalToday() = %v, want %v",
					got,
					tt.want,
				)
			}
		})
	}
}

func TestHotelLocalTodayInvalidTimezone(t *testing.T) {
	_, err := hotelLocalToday(
		time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC),
		"Invalid/Timezone",
	)

	if err == nil {
		t.Fatal("expected an error for invalid timezone")
	}
}
