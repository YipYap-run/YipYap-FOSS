package jwks

import (
	"context"
	"testing"
	"time"
)

func TestStartRotationLoop_RunsImmediatelyAndExitsOnCancel(t *testing.T) {
	m, st := newTestManager(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var calls int
	go func() {
		defer close(done)
		StartRotationLoop(ctx, m, time.Hour, func(err error) {
			if err != nil {
				t.Errorf("rotate err: %v", err)
			}
			calls++
		})
	}()

	// Wait until the first tick fires, then cancel. Read via ListAll so
	// the poll is serialized against the rotation goroutine's Save.
	deadline := time.After(2 * time.Second)
	for {
		rows, _ := st.ListAll(context.Background())
		if len(rows) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("first rotate did not fire in time")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("loop did not exit on cancel")
	}
	if calls < 1 {
		t.Fatalf("expected at least 1 onRotate call, got %d", calls)
	}
	if len(st.rows) != 1 {
		t.Fatalf("expected 1 key, got %d", len(st.rows))
	}
}
