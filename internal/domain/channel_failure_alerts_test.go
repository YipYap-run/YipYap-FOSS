package domain

import (
	"testing"
	"time"
)

func TestValidateChannelFailureAlertThreshold(t *testing.T) {
	for _, n := range []int{1, 3, 20} {
		if err := ValidateChannelFailureAlertThreshold(n); err != nil {
			t.Errorf("%d: unexpected error %v", n, err)
		}
	}
	for _, n := range []int{-1, 0, 21, 1000} {
		if err := ValidateChannelFailureAlertThreshold(n); err == nil {
			t.Errorf("%d: accepted", n)
		}
	}
}

func TestChannelFailureAlertThresholdFromSettings(t *testing.T) {
	cases := map[string]struct {
		settings map[string]string
		want     int
	}{
		"missing":  {nil, 3},
		"set":      {map[string]string{SettingChannelFailureAlertThreshold: "5"}, 5},
		"spaces":   {map[string]string{SettingChannelFailureAlertThreshold: " 7 "}, 7},
		"garbage":  {map[string]string{SettingChannelFailureAlertThreshold: "lots"}, 3},
		"too low":  {map[string]string{SettingChannelFailureAlertThreshold: "0"}, 3},
		"too high": {map[string]string{SettingChannelFailureAlertThreshold: "21"}, 3},
	}
	for name, tc := range cases {
		if got := ChannelFailureAlertThresholdFromSettings(tc.settings); got != tc.want {
			t.Errorf("%s: got %d, want %d", name, got, tc.want)
		}
	}
}

func TestHealthReportsOwnerNotice(t *testing.T) {
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	ch := &NotificationChannel{ConsecutiveFailures: 3, LastError: "x", FailureNotifiedAt: &at}
	if h := ch.Health(); h.OwnersNotifiedAt == nil || !h.OwnersNotifiedAt.Equal(at) {
		t.Fatalf("failing health = %+v, want owners_notified_at", h)
	}
	ch.ConsecutiveFailures = 0
	ch.LastSuccessAt = &at
	if h := ch.Health(); h.OwnersNotifiedAt != nil {
		t.Fatalf("ok health = %+v, want no owner notice", h)
	}
}
