package reply

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
)

func makeEvent(t *testing.T, id, ceType string, data any) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetID(id)
	ev.SetType(ceType)
	ev.SetSource("test://source")
	if data != nil {
		if err := ev.SetData(ce.ApplicationJSON, data); err != nil {
			t.Fatalf("set data: %v", err)
		}
	}
	return ev
}

func TestMemoryRegistry_RecordAndLookup(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	now := time.Unix(1_700_000_000, 0).UTC()
	reg.SetClock(func() time.Time { return now })

	ev := makeEvent(t, "evt-1", "run.yipyap.alert.fired.v1", map[string]any{
		"alert_id":   "alert-7",
		"monitor_id": "mon-3",
	})
	if err := reg.Record(context.Background(), ev, "org-1", "chan-A"); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, ok := reg.Lookup(context.Background(), "evt-1")
	if !ok {
		t.Fatalf("expected entry for evt-1")
	}
	if got.EventID != "evt-1" || got.OrgID != "org-1" || got.ChannelID != "chan-A" {
		t.Errorf("entry ids mismatch: %+v", got)
	}
	if got.AlertID != "alert-7" || got.MonitorID != "mon-3" {
		t.Errorf("entry data ids mismatch: alert=%q monitor=%q", got.AlertID, got.MonitorID)
	}
	if !got.EmittedAt.Equal(now) {
		t.Errorf("emittedAt=%v want %v", got.EmittedAt, now)
	}
}

func TestMemoryRegistry_MissingID_ReturnsFalse(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	if _, ok := reg.Lookup(context.Background(), "nope"); ok {
		t.Fatalf("expected miss")
	}
	if _, ok := reg.Lookup(context.Background(), ""); ok {
		t.Fatalf("empty id should miss")
	}
}

func TestMemoryRegistry_TTLExpiry(t *testing.T) {
	reg := NewMemoryRegistry(100, 10*time.Minute)
	cur := time.Unix(1_700_000_000, 0).UTC()
	reg.SetClock(func() time.Time { return cur })

	ev := makeEvent(t, "evt-1", "run.yipyap.alert.fired.v1", nil)
	if err := reg.Record(context.Background(), ev, "org-1", "chan-A"); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Just before TTL: still present.
	cur = cur.Add(9*time.Minute + 59*time.Second)
	if _, ok := reg.Lookup(context.Background(), "evt-1"); !ok {
		t.Fatalf("should still be present under TTL")
	}

	// Past TTL: gone.
	cur = cur.Add(2 * time.Second)
	if _, ok := reg.Lookup(context.Background(), "evt-1"); ok {
		t.Fatalf("should have expired")
	}
}

func TestMemoryRegistry_LRUEviction(t *testing.T) {
	reg := NewMemoryRegistry(3, time.Hour)

	for i := 0; i < 5; i++ {
		ev := makeEvent(t, fmt.Sprintf("evt-%d", i), "t", nil)
		if err := reg.Record(context.Background(), ev, "org", "chan"); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	if n := reg.Len(); n != 3 {
		t.Fatalf("len=%d want 3", n)
	}
	// evt-0 and evt-1 should be evicted.
	for _, id := range []string{"evt-0", "evt-1"} {
		if _, ok := reg.Lookup(context.Background(), id); ok {
			t.Errorf("%s should have been evicted", id)
		}
	}
	for _, id := range []string{"evt-2", "evt-3", "evt-4"} {
		if _, ok := reg.Lookup(context.Background(), id); !ok {
			t.Errorf("%s should be present", id)
		}
	}
}

func TestMemoryRegistry_EmptyIDRejected(t *testing.T) {
	reg := NewMemoryRegistry(10, time.Hour)
	ev := ce.NewEvent()
	ev.SetType("t")
	ev.SetSource("s")
	if err := reg.Record(context.Background(), ev, "org", "chan"); err == nil {
		t.Fatalf("expected error for empty id")
	}
}

func TestMemoryRegistry_Concurrent(t *testing.T) {
	const goroutines = 16
	const perG = 200
	// Capacity headroom: total writes are goroutines * perG = 3200. The
	// LRU evicts the oldest entry when the cap is hit, so any cap <= 3200
	// means a lookup immediately after a write CAN race against another
	// goroutine's eviction and miss. Sized at total*2 for headroom so
	// concurrent write + lookup is deterministic. Earlier rev sized at
	// 1000 with a comment claiming "capacity > goroutines*perG", wrong;
	// it manifested as a ~30% flake under -race -count=10.
	reg := NewMemoryRegistry(goroutines*perG*2, time.Hour)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				ev := makeEvent(t, id, "t", nil)
				if err := reg.Record(context.Background(), ev, "org", "chan"); err != nil {
					t.Errorf("record: %v", err)
					return
				}
				if _, ok := reg.Lookup(context.Background(), id); !ok {
					t.Errorf("lookup miss for %s", id)
					return
				}
			}
		}()
	}
	wg.Wait()
}
