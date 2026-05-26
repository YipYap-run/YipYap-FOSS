package oidc

import (
	"testing"
	"time"
)

func TestVerifierCache_HitAndMiss(t *testing.T) {
	c := NewVerifierCache(time.Hour)
	cfg := VerifierConfig{
		Issuer:    "https://idp.example.com",
		Audiences: []string{"aud-a"},
	}

	builds := 0
	build := func() *Verifier {
		builds++
		return NewVerifier(cfg)
	}

	v1 := c.Get(cfg, build)
	v2 := c.Get(cfg, build)
	if v1 != v2 {
		t.Fatalf("expected second Get to return the cached verifier, got distinct instances")
	}
	if builds != 1 {
		t.Fatalf("expected build to run exactly once, ran %d times", builds)
	}
	if got := c.Size(); got != 1 {
		t.Fatalf("Size=%d, want 1", got)
	}
}

func TestVerifierCache_TTLEviction(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	c := NewVerifierCache(5 * time.Minute).WithClock(func() time.Time { return now })
	cfg := VerifierConfig{Issuer: "https://idp.example.com"}

	builds := 0
	build := func() *Verifier {
		builds++
		return NewVerifier(cfg)
	}

	_ = c.Get(cfg, build)
	// Advance past TTL, next Get must rebuild.
	now = now.Add(6 * time.Minute)
	_ = c.Get(cfg, build)
	if builds != 2 {
		t.Fatalf("expected 2 builds after TTL advance, got %d", builds)
	}
}

func TestVerifierCache_Invalidate(t *testing.T) {
	c := NewVerifierCache(time.Hour)
	cfg := VerifierConfig{Issuer: "https://idp.example.com"}
	build := func() *Verifier { return NewVerifier(cfg) }

	_ = c.Get(cfg, build)
	c.Invalidate(cfg)
	if got := c.Size(); got != 0 {
		t.Fatalf("Size=%d after Invalidate, want 0", got)
	}
}

func TestVerifierCache_KeyByAudienceSetOrderInsensitive(t *testing.T) {
	c := NewVerifierCache(time.Hour)
	cfgA := VerifierConfig{Issuer: "https://idp", Audiences: []string{"x", "y"}}
	cfgB := VerifierConfig{Issuer: "https://idp", Audiences: []string{"y", "x"}}
	builds := 0
	build := func() *Verifier {
		builds++
		return NewVerifier(VerifierConfig{})
	}
	_ = c.Get(cfgA, build)
	_ = c.Get(cfgB, build)
	if builds != 1 {
		t.Fatalf("audience order must not split keys: %d builds", builds)
	}
}
