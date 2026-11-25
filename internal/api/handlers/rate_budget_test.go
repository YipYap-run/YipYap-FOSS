package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func TestRateBudgetMemoryFallback(t *testing.T) {
	b := NewRateBudget(nil)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		count, _, err := b.Take(ctx, "k", time.Minute)
		if err != nil || count != i {
			t.Fatalf("take %d = %d, %v", i, count, err)
		}
	}
	count, _, err := b.Peek(ctx, "k", time.Minute)
	if err != nil || count != 3 {
		t.Fatalf("peek = %d, %v", count, err)
	}
}

func TestRateBudgetIsSharedThroughTheStore(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	first := NewRateBudget(s)
	second := NewRateBudget(s)
	for i := 1; i <= 3; i++ {
		if count, _, err := first.Take(ctx, "survey:203.0.113.5", time.Minute); err != nil || count != i {
			t.Fatalf("take %d = %d, %v", i, count, err)
		}
	}
	count, _, err := second.Peek(ctx, "survey:203.0.113.5", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("second replica sees %d, want 3", count)
	}
}

func TestBudgetLimitMiddleware(t *testing.T) {
	b := NewRateBudget(nil)
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	key := func(*http.Request) (string, error) { return "203.0.113.9", nil }
	h := BudgetLimit(b, "survey", 2, time.Minute, key, nil)(next)

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/support/survey", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/support/survey", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After header")
	}
	var body struct {
		Code    string `json:"code"`
		RetryIn int    `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "rate_limited" || body.RetryIn < 1 {
		t.Errorf("body = %+v", body)
	}
	if calls != 2 {
		t.Errorf("handler ran %d times, want 2", calls)
	}
}
