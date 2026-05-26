package jwks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type testObserver struct {
	signs    atomic.Int64
	rotates  atomic.Int64
	lastErr  error
	lastCnt  int
	lastAge  time.Duration
}

func (t *testObserver) OnSign(_ Algorithm, _ string) { t.signs.Add(1) }
func (t *testObserver) OnRotate(err error, cnt int, age time.Duration) {
	t.rotates.Add(1)
	t.lastErr, t.lastCnt, t.lastAge = err, cnt, age
}

func TestObserver_RotationAndSign(t *testing.T) {
	store := newMemStore()
	obs := &testObserver{}
	m := NewManager(store, WithObserver(obs))
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if obs.rotates.Load() != 1 {
		t.Fatalf("expected 1 rotate callback, got %d", obs.rotates.Load())
	}
	if obs.lastErr != nil {
		t.Fatalf("lastErr: %v", obs.lastErr)
	}
	if obs.lastCnt != 1 {
		t.Fatalf("lastCnt: %d", obs.lastCnt)
	}

	signer, err := m.GetActiveSigner(ctx)
	if err != nil {
		t.Fatalf("GetActiveSigner: %v", err)
	}
	now := time.Now().UTC()
	if _, err := signer.Sign(Claims{Issuer: "i", Subject: "s", Audience: []string{"a"}, IssuedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if obs.signs.Load() != 1 {
		t.Fatalf("expected 1 sign callback, got %d", obs.signs.Load())
	}
}
