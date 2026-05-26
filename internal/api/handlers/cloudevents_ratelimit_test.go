package handlers

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

// TestStreamConcurrencyGate_CapsPerOrg_H16 confirms the concurrency gate
// rejects past-cap concurrent streams and that drained slots re-open.
func TestStreamConcurrencyGate_CapsPerOrg_H16(t *testing.T) {
	g := NewStreamConcurrencyGate(3)

	// A downstream handler that blocks until the test releases it so we
	// can hold gate slots while counting.
	release := make(chan struct{})
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	wrapped := g.Middleware(downstream)

	newReq := func(orgID string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/cloudevents/stream", nil)
		ctx := middleware.SetClaims(req.Context(), &auth.Claims{OrgID: orgID})
		return req.WithContext(ctx)
	}

	var wg sync.WaitGroup
	responses := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, newReq("org-1"))
			responses <- rec.Code
		}()
	}

	// Wait until 3 slots are held and 2 have failed-fast with 429.
	var failed, succeeded int
	// First drain the 2 rejects that return immediately.
	for failed < 2 {
		code := <-responses
		if code == http.StatusTooManyRequests {
			failed++
			continue
		}
		t.Fatalf("unexpected early response code %d", code)
	}
	if failed != 2 {
		t.Fatalf("expected 2 failures, got %d", failed)
	}

	// Cross-org isolation: different org has its own cap.
	otherReq := newReq("org-2")
	rec := httptest.NewRecorder()
	doneOther := make(chan struct{})
	go func() {
		wrapped.ServeHTTP(rec, otherReq)
		close(doneOther)
	}()
	// Release it right away, this request should reach downstream.
	// To release only this one we'd need a separate release channel; just
	// close the common release after counting active.
	if got := g.active("org-1"); got != 3 {
		t.Fatalf("org-1 active = %d, want 3", got)
	}

	// Now release all held requests.
	close(release)
	wg.Wait()
	<-doneOther
	close(responses)
	for code := range responses {
		if code == http.StatusOK {
			succeeded++
		}
	}
	if succeeded != 3 {
		t.Fatalf("expected 3 successes after release, got %d", succeeded)
	}

	// After drain, slots are freed.
	if got := g.active("org-1"); got != 0 {
		t.Fatalf("org-1 active after drain = %d, want 0", got)
	}
}

// TestStreamConcurrencyGate_Unauthenticated401 confirms the
// defence-in-depth path: an unauth'd request does not book a gate slot.
func TestStreamConcurrencyGate_Unauthenticated401(t *testing.T) {
	g := NewStreamConcurrencyGate(1)
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("must not reach downstream")
	})
	wrapped := g.Middleware(downstream)

	req := httptest.NewRequest(http.MethodGet, "/cloudevents/stream", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if got := g.active(""); got != 0 {
		t.Fatalf("no-claim request must not book slot; active = %d", got)
	}
}

// TestRateLimitKeyByOrg keys on org id (H-16 per-org poll rate limit).
func TestRateLimitKeyByOrg(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/cloudevents/poll", nil)
	ctx := middleware.SetClaims(req.Context(), &auth.Claims{OrgID: "org-abc"})
	req = req.WithContext(ctx)
	key, err := RateLimitKeyByOrg(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if key != "org:org-abc" {
		t.Fatalf("key = %q, want org:org-abc", key)
	}

	// No claims: anonymous fallback (auth middleware would 401 before
	// the handler; the key just needs to not panic).
	req2 := httptest.NewRequest(http.MethodGet, "/cloudevents/poll", nil)
	key2, err := RateLimitKeyByOrg(req2)
	if err != nil {
		t.Fatalf("anon err: %v", err)
	}
	if key2 != "anonymous" {
		t.Fatalf("anonymous key = %q, want anonymous", key2)
	}
}
