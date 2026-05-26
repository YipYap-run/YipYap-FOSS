package jwks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWKSHandler_ServesActivePlusRetiring(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	m, _ := newTestManager(t, func() time.Time { return cur })
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	cur = now.Add(8 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}

	h := NewJWKSHandler(m)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Keys) != 2 {
		t.Fatalf("expected 2 keys (active + retiring), got %d", len(body.Keys))
	}
}

func TestJWKSHandler_ExcludesExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	m, _ := newTestManager(t, func() time.Time { return cur })
	ctx := context.Background()

	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	cur = now.Add(8 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	cur = now.Add(25 * 24 * time.Hour)
	if err := m.Rotate(ctx); err != nil {
		t.Fatalf("Rotate 3: %v", err)
	}

	h := NewJWKSHandler(m)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Keys []map[string]any `json:"keys"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	// With the clock at +25d, the original key's retired_at (day 8) is
	// more than 14d old → demoted to expired. So public set must contain
	// at most the 1 active + 1 retiring from Rotate-3, never 3.
	for _, k := range body.Keys {
		_ = k
	}
	if len(body.Keys) > 2 {
		t.Fatalf("expected <=2 keys, got %d (expired leaked)", len(body.Keys))
	}
}

func TestJWKSHandler_CacheControl(t *testing.T) {
	m, _ := newTestManager(t, nil)
	if err := m.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	h := NewJWKSHandler(m)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Fatalf("cache-control: %q", got)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: %q", got)
	}
}

func TestJWKSHandler_405(t *testing.T) {
	m, _ := newTestManager(t, nil)
	if err := m.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	h := NewJWKSHandler(m)
	req := httptest.NewRequest(http.MethodPost, "/.well-known/jwks.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "GET" {
		t.Fatalf("allow: %q", got)
	}
}

func TestJWKSHandler_503_WhenNoActiveKey(t *testing.T) {
	m, _ := newTestManager(t, nil)
	h := NewJWKSHandler(m)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d", rr.Code)
	}
}
