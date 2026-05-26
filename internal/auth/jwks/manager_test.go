package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"
)

func newTestManager(t *testing.T, clock func() time.Time) (*Manager, *memKeyStore) {
	t.Helper()
	store := newMemStore()
	opts := []Option{}
	if clock != nil {
		opts = append(opts, WithClock(clock))
	}
	return NewManager(store, opts...), store
}

func TestGenerate_EdDSA(t *testing.T) {
	m, _ := newTestManager(t, nil)
	k, err := m.Generate(EdDSA)
	if err != nil {
		t.Fatalf("Generate EdDSA: %v", err)
	}
	if k.Algorithm != EdDSA {
		t.Fatalf("alg: %v", k.Algorithm)
	}
	if _, ok := k.Private.(ed25519.PrivateKey); !ok {
		t.Fatalf("private key type: %T", k.Private)
	}
	var jwk map[string]any
	if err := json.Unmarshal([]byte(k.PublicJWK), &jwk); err != nil {
		t.Fatalf("decode public jwk: %v", err)
	}
	if jwk["kty"] != "OKP" {
		t.Fatalf("kty: %v", jwk["kty"])
	}
	if jwk["kid"] != k.KID {
		t.Fatalf("kid mismatch: jwk=%v key=%v", jwk["kid"], k.KID)
	}
	if jwk["alg"] != "EdDSA" {
		t.Fatalf("alg in jwk: %v", jwk["alg"])
	}
}

// TestGenerate_AllAlgorithms exercises Generate across every supported
// algorithm, asserting the resulting private-key Go type and the JWK
// shape (kty plus, for ECDSA, the curve name).
func TestGenerate_AllAlgorithms(t *testing.T) {
	cases := []struct {
		alg     Algorithm
		kty     string
		crv     string // only set for EC
		privFn  func(any) bool
	}{
		{EdDSA, "OKP", "", func(k any) bool { _, ok := k.(ed25519.PrivateKey); return ok }},
		{RS256, "RSA", "", func(k any) bool { _, ok := k.(*rsa.PrivateKey); return ok }},
		{RS384, "RSA", "", func(k any) bool { _, ok := k.(*rsa.PrivateKey); return ok }},
		{RS512, "RSA", "", func(k any) bool { _, ok := k.(*rsa.PrivateKey); return ok }},
		{PS256, "RSA", "", func(k any) bool { _, ok := k.(*rsa.PrivateKey); return ok }},
		{PS384, "RSA", "", func(k any) bool { _, ok := k.(*rsa.PrivateKey); return ok }},
		{PS512, "RSA", "", func(k any) bool { _, ok := k.(*rsa.PrivateKey); return ok }},
		{ES256, "EC", "P-256", func(k any) bool { _, ok := k.(*ecdsa.PrivateKey); return ok }},
		{ES384, "EC", "P-384", func(k any) bool { _, ok := k.(*ecdsa.PrivateKey); return ok }},
		{ES512, "EC", "P-521", func(k any) bool { _, ok := k.(*ecdsa.PrivateKey); return ok }},
	}
	for _, tc := range cases {
		t.Run(string(tc.alg), func(t *testing.T) {
			m, _ := newTestManager(t, nil)
			k, err := m.Generate(tc.alg)
			if err != nil {
				t.Fatalf("Generate(%s): %v", tc.alg, err)
			}
			if k.Algorithm != tc.alg {
				t.Fatalf("alg: got %v want %v", k.Algorithm, tc.alg)
			}
			if !tc.privFn(k.Private) {
				t.Fatalf("private key type: %T", k.Private)
			}
			var jwk map[string]any
			if err := json.Unmarshal([]byte(k.PublicJWK), &jwk); err != nil {
				t.Fatalf("decode jwk: %v", err)
			}
			if jwk["kty"] != tc.kty {
				t.Fatalf("kty: got %v want %v", jwk["kty"], tc.kty)
			}
			if jwk["alg"] != string(tc.alg) {
				t.Fatalf("alg in jwk: got %v want %v", jwk["alg"], tc.alg)
			}
			if jwk["kid"] != k.KID {
				t.Fatalf("kid mismatch: jwk=%v key=%v", jwk["kid"], k.KID)
			}
			if tc.crv != "" && jwk["crv"] != tc.crv {
				t.Fatalf("crv: got %v want %v", jwk["crv"], tc.crv)
			}
		})
	}
}

func TestGenerate_RS256(t *testing.T) {
	m, _ := newTestManager(t, nil)
	k, err := m.Generate(RS256)
	if err != nil {
		t.Fatalf("Generate RS256: %v", err)
	}
	if _, ok := k.Private.(*rsa.PrivateKey); !ok {
		t.Fatalf("private key type: %T", k.Private)
	}
	var jwk map[string]any
	if err := json.Unmarshal([]byte(k.PublicJWK), &jwk); err != nil {
		t.Fatalf("decode jwk: %v", err)
	}
	if jwk["kty"] != "RSA" {
		t.Fatalf("kty: %v", jwk["kty"])
	}
}

func TestGenerate_ES256(t *testing.T) {
	m, _ := newTestManager(t, nil)
	k, err := m.Generate(ES256)
	if err != nil {
		t.Fatalf("Generate ES256: %v", err)
	}
	if _, ok := k.Private.(*ecdsa.PrivateKey); !ok {
		t.Fatalf("private key type: %T", k.Private)
	}
	var jwk map[string]any
	if err := json.Unmarshal([]byte(k.PublicJWK), &jwk); err != nil {
		t.Fatalf("decode jwk: %v", err)
	}
	if jwk["kty"] != "EC" {
		t.Fatalf("kty: %v", jwk["kty"])
	}
	if jwk["crv"] != "P-256" {
		t.Fatalf("crv: %v", jwk["crv"])
	}
}

func TestGenerate_UnsupportedAlg(t *testing.T) {
	m, _ := newTestManager(t, nil)
	if _, err := m.Generate(Algorithm("HS256")); err == nil {
		t.Fatalf("expected error for HS256")
	}
}

func TestRotate_FirstTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	m, _ := newTestManager(t, clock)
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	public, err := m.ListPublicJWKs(ctx)
	if err != nil {
		t.Fatalf("ListPublicJWKs: %v", err)
	}
	if len(public) != 1 {
		t.Fatalf("expected 1 public key, got %d", len(public))
	}
}

func TestRotate_Idempotent_SameWeek(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	m, st := newTestManager(t, clock)
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	firstCount := len(st.rows)
	if firstCount != 1 {
		t.Fatalf("expected 1 row after first rotate, got %d", firstCount)
	}

	// Advance clock by 2 minutes and rotate again, should be a no-op.
	nowPlus := now.Add(2 * time.Minute)
	m.clock = func() time.Time { return nowPlus }
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	if len(st.rows) != 1 {
		t.Fatalf("expected still 1 row, got %d", len(st.rows))
	}
}

func TestRotate_NextWeek(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	clock := func() time.Time { return cur }
	m, st := newTestManager(t, clock)
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	// Advance 8 days.
	cur = now.Add(8 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}

	// Now: one active, one retiring.
	var actives, retirings int
	for _, k := range st.rows {
		switch k.Status {
		case StatusActive:
			actives++
		case StatusRetiring:
			retirings++
		}
	}
	if actives != 1 || retirings != 1 {
		t.Fatalf("expected 1 active + 1 retiring, got active=%d retiring=%d", actives, retirings)
	}
}

func TestRotate_OverlapExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	clock := func() time.Time { return cur }
	m, st := newTestManager(t, clock)
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	// Advance 8 days -> old is retiring.
	cur = now.Add(8 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	// Advance to 8 + 15 = 23 days past start. Retired-at was set at day 8,
	// so retiring->expired should fire after day 22 (>14d).
	cur = now.Add(23 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 3: %v", err)
	}

	// Expect: at least one expired, the latest active key should still be active.
	var expired, active int
	for _, k := range st.rows {
		switch k.Status {
		case StatusActive:
			active++
		case StatusExpired:
			expired++
		}
	}
	if active < 1 {
		t.Fatalf("expected at least 1 active, got %d", active)
	}
	if expired < 1 {
		t.Fatalf("expected at least 1 expired, got %d", expired)
	}

	// ListPublicJWKs should not include any expired key.
	public, err := m.ListPublicJWKs(ctx)
	if err != nil {
		t.Fatalf("ListPublicJWKs: %v", err)
	}
	if len(public) > 2 {
		t.Fatalf("public set grew unexpectedly: %d", len(public))
	}
}

func TestRotate_ExpiredPrune(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	clock := func() time.Time { return cur }
	m, st := newTestManager(t, clock)
	ctx := context.Background()

	// Boot + rotate into a retiring/active pair.
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	cur = now.Add(8 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}

	// Jump well past the prune cutoff: 45 days later.
	cur = now.Add(45 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 3: %v", err)
	}

	// The original retiring-then-expired key should be pruned.
	var totalRows int
	for _, k := range st.rows {
		// Not strictly relevant, but exercise the map.
		_ = k
		totalRows++
	}
	if totalRows > 2 {
		t.Fatalf("expected prune to bring store <=2 rows, got %d", totalRows)
	}
}

func TestManager_GetActiveSigner_NoKey(t *testing.T) {
	m, _ := newTestManager(t, nil)
	if _, err := m.GetActiveSigner(context.Background()); err == nil {
		t.Fatalf("expected ErrNoActiveKey, got nil")
	}
}
