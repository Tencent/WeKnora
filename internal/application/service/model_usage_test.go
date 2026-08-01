package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUsageWindowUsesTwoHourBuckets(t *testing.T) {
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		value     string
		wantStart time.Time
	}{
		{name: "15 minutes", value: "15m", wantStart: now.Add(-15 * time.Minute)},
		{name: "1 hour", value: "1h", wantStart: now.Add(-time.Hour)},
		{name: "24 hours default", value: "24h", wantStart: now.Add(-24 * time.Hour)},
		{name: "7 days", value: "7d", wantStart: now.Add(-7 * 24 * time.Hour)},
		{name: "unknown defaults to 24 hours", value: "unknown", wantStart: now.Add(-24 * time.Hour)},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			start, bucket := usageWindow(tt.value, now)

			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, 2*time.Hour, bucket)
		})
	}
}
