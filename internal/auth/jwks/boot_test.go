package jwks

import (
	"context"
	"testing"
)

func TestEnsureInitialKey_EmptyStore(t *testing.T) {
	m, st := newTestManager(t, nil)
	if err := EnsureInitialKey(context.Background(), m); err != nil {
		t.Fatalf("EnsureInitialKey: %v", err)
	}
	if len(st.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(st.rows))
	}
	public, err := m.ListPublicJWKs(context.Background())
	if err != nil {
		t.Fatalf("ListPublicJWKs: %v", err)
	}
	if len(public) != 1 {
		t.Fatalf("expected 1 public, got %d", len(public))
	}
}

func TestEnsureInitialKey_Noop_WhenKeysExist(t *testing.T) {
	m, st := newTestManager(t, nil)
	ctx := context.Background()
	// Seed with a rotate first.
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	originalCount := len(st.rows)

	// EnsureInitialKey should be a no-op now.
	if err := EnsureInitialKey(ctx, m); err != nil {
		t.Fatalf("EnsureInitialKey: %v", err)
	}
	if len(st.rows) != originalCount {
		t.Fatalf("EnsureInitialKey mutated store: %d -> %d", originalCount, len(st.rows))
	}
}

func TestJWKThumbprint(t *testing.T) {
	m, _ := newTestManager(t, nil)
	k, err := m.Generate(EdDSA)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	thumb, err := jwkThumbprint(k.PublicJWK)
	if err != nil {
		t.Fatalf("jwkThumbprint: %v", err)
	}
	if len(thumb) == 0 {
		t.Fatalf("empty thumbprint")
	}
}
