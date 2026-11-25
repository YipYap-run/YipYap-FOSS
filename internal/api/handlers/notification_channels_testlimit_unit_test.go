package handlers

import (
	"testing"
	"time"
)

func TestChannelTestLimiterSlidingWindow(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	l := newChannelTestLimiter()
	l.now = func() time.Time { return now }

	for i := range channelTestLimit {
		if ok, _, _ := l.allow("org-a", "webhook"); !ok {
			t.Fatalf("test %d refused under the cap", i+1)
		}
		now = now.Add(time.Second)
	}
	ok, wait, scope := l.allow("org-a", "webhook")
	if ok {
		t.Fatal("test over the per-minute cap allowed")
	}
	// The first hit was 10s ago, so a slot frees in 50s.
	if wait != 50*time.Second || scope != testLimitScopeOrg {
		t.Fatalf("retry after %v scope %q, want 50s org", wait, scope)
	}
	if ok, _, _ := l.allow("org-b", "webhook"); !ok {
		t.Fatal("another org shares the cap")
	}
	now = now.Add(wait)
	if ok, _, _ := l.allow("org-a", "webhook"); !ok {
		t.Fatal("slot did not free after the window")
	}
}

func TestChannelTestLimiterRefusalDoesNotCount(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	l := newChannelTestLimiter()
	l.now = func() time.Time { return now }
	for range channelTestLimit {
		_, _, _ = l.allow("org-a", "webhook")
	}
	for range 50 {
		_, _, _ = l.allow("org-a", "webhook")
	}
	now = now.Add(channelTestWindow + time.Second)
	if ok, _, _ := l.allow("org-a", "webhook"); !ok {
		t.Fatal("refused attempts extended the window")
	}
}
