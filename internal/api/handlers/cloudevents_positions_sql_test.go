package handlers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func TestCESQLPositionStore_CapsPerOrg(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	positions := newCESQLPositionStore(s.CloudEventPositions(), time.Hour, 3)
	now := time.Now().UTC()
	positions.clock = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		positions.save(ctx, "org-1", fmt.Sprintf("k%d", i), ceCursor{E: "e", S: uint64(i)})
	}

	for _, key := range []string{"k0", "k1"} {
		if _, ok := positions.load(ctx, "org-1", key); ok {
			t.Fatalf("oldest key %q survived the cap", key)
		}
	}
	for i := 2; i < 5; i++ {
		c, ok := positions.load(ctx, "org-1", fmt.Sprintf("k%d", i))
		if !ok || c.S != uint64(i) {
			t.Fatalf("k%d = %+v, %v; want it retained", i, c, ok)
		}
	}

	now = now.Add(time.Second)
	positions.save(ctx, "org-1", "k4", ceCursor{E: "e", S: 44})
	if c, ok := positions.load(ctx, "org-1", "k2"); !ok || c.S != 2 {
		t.Fatalf("updating an existing key dropped another: k2 = %+v, %v", c, ok)
	}
}
