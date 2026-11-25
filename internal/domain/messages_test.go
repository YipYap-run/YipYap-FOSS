package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCheckRequestStale(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		interval int
		age      time.Duration
		zero     bool
		want     bool
	}{
		{"fresh", 60, 5 * time.Second, false, false},
		{"just under one interval", 60, 59 * time.Second, false, false},
		{"older than one interval", 60, 61 * time.Second, false, true},
		{"hour-old backlog", 60, time.Hour, false, true},
		{"short interval uses floor", 10, 20 * time.Second, false, false},
		{"short interval past floor", 10, 31 * time.Second, false, true},
		{"long interval", 3600, 30 * time.Minute, false, false},
		{"issued in the future (skew)", 60, -10 * time.Second, false, false},
		{"no IssuedAt from old publisher", 60, 0, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := CheckRequest{IntervalSeconds: tc.interval}
			if !tc.zero {
				r.IssuedAt = now.Add(-tc.age)
			}
			if got := r.Stale(now); got != tc.want {
				t.Fatalf("Stale = %v, want %v (stale after %s)", got, tc.want, r.StaleAfter())
			}
		})
	}
}

// TestCheckRequestIssuedAtWire checks that the field round-trips and that a
// payload from an old publisher (no issued_at) decodes to the zero time.
func TestCheckRequestIssuedAtWire(t *testing.T) {
	ts := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(CheckRequest{MonitorID: "m", IssuedAt: ts})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"issued_at":"2026-11-24T12:00:00Z"`) {
		t.Fatalf("encoded = %s", data)
	}
	var back CheckRequest
	if err := json.Unmarshal(data, &back); err != nil || !back.IssuedAt.Equal(ts) {
		t.Fatalf("decoded %v err %v", back.IssuedAt, err)
	}

	empty, _ := json.Marshal(CheckRequest{MonitorID: "m"})
	if strings.Contains(string(empty), "issued_at") {
		t.Fatalf("zero IssuedAt encoded: %s", empty)
	}
	var old CheckRequest
	if err := json.Unmarshal([]byte(`{"monitor_id":"m","interval_seconds":60}`), &old); err != nil {
		t.Fatal(err)
	}
	if !old.IssuedAt.IsZero() || old.Stale(time.Now()) {
		t.Fatal("request without issued_at must not be stale")
	}
}
