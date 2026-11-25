package sqlite

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// The owner email for a failing channel is claimed once per streak: not
// below the threshold, once at or above it even under concurrency, and
// again only after a success has ended the streak.
func TestChannelFailureNoticeClaim(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "slack", Name: "ops", Config: "{}", Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	chs := s.NotificationChannels()
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if _, err := chs.RecordDeliveryFailure(ctx, ch.ID, at, "Delivery failed."); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := chs.ClaimFailureNotice(ctx, ch.ID, 3, at); err != nil || ok {
		t.Fatalf("below threshold: ok=%v err=%v", ok, err)
	}
	if _, err := chs.RecordDeliveryFailure(ctx, ch.ID, at, "Delivery failed."); err != nil {
		t.Fatal(err)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := chs.ClaimFailureNotice(ctx, ch.ID, 3, at)
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("concurrent claims won %d times, want 1", wins.Load())
	}
	got, _ := chs.GetByID(ctx, ch.ID)
	if got.FailureNotifiedAt == nil || !got.FailureNotifiedAt.Equal(at) {
		t.Fatalf("failure_notified_at = %v, want %v", got.FailureNotifiedAt, at)
	}

	// More failures in the same streak do not claim again.
	if _, err := chs.RecordDeliveryFailure(ctx, ch.ID, at, "Delivery failed."); err != nil {
		t.Fatal(err)
	}
	if ok, _ := chs.ClaimFailureNotice(ctx, ch.ID, 3, at); ok {
		t.Fatal("claimed twice in one streak")
	}

	// A success ends the streak, reports the recovery once, and clears
	// the notice.
	if rec, err := chs.RecordDeliverySuccess(ctx, ch.ID, at.Add(time.Minute)); err != nil || !rec {
		t.Fatalf("first success: recovered=%v err=%v", rec, err)
	}
	if rec, _ := chs.RecordDeliverySuccess(ctx, ch.ID, at.Add(2*time.Minute)); rec {
		t.Fatal("second success reported a recovery again")
	}
	got, _ = chs.GetByID(ctx, ch.ID)
	if got.FailureNotifiedAt != nil || got.ConsecutiveFailures != 0 {
		t.Fatalf("after success: notified=%v failures=%d", got.FailureNotifiedAt, got.ConsecutiveFailures)
	}

	// The next streak can claim again once it reaches the threshold.
	for i := 0; i < 3; i++ {
		if _, err := chs.RecordDeliveryFailure(ctx, ch.ID, at, "Delivery failed."); err != nil {
			t.Fatal(err)
		}
	}
	later := at.Add(time.Hour)
	if ok, err := chs.ClaimFailureNotice(ctx, ch.ID, 3, later); err != nil || !ok {
		t.Fatalf("new streak: ok=%v err=%v", ok, err)
	}

	// Release only undoes the caller's own claim.
	if err := chs.ReleaseFailureNotice(ctx, ch.ID, at); err != nil {
		t.Fatal(err)
	}
	if got, _ = chs.GetByID(ctx, ch.ID); got.FailureNotifiedAt == nil {
		t.Fatal("release with a stale claim time cleared the notice")
	}
	if err := chs.ReleaseFailureNotice(ctx, ch.ID, later); err != nil {
		t.Fatal(err)
	}
	if got, _ = chs.GetByID(ctx, ch.ID); got.FailureNotifiedAt != nil {
		t.Fatal("release did not clear the notice")
	}
}
