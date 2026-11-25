package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// channelTestFixture returns a store with an org on plan, and a func that
// creates a channel and one that runs Test on a channel id.
func channelTestFixture(t *testing.T, plan domain.OrgPlan) (*sqlite.SQLiteStore, *domain.Org, *capturingTestSender, func(typ, config string) *domain.NotificationChannel, func(id string) *httptest.ResponseRecorder) {
	t.Helper()
	return channelTestFixtureWrapped(t, plan, nil)
}

// channelTestFixtureWrapped is channelTestFixture with the handler's store
// passed through wrap, so a test can inject store failures.
func channelTestFixtureWrapped(t *testing.T, plan domain.OrgPlan, wrap func(store.Store) store.Store) (*sqlite.SQLiteStore, *domain.Org, *capturingTestSender, func(typ, config string) *domain.NotificationChannel, func(id string) *httptest.ResponseRecorder) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	org := &domain.Org{ID: uuid.NewString(), Name: "Acme", Slug: "acme-" + uuid.NewString()[:8], Plan: plan}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	sender := &capturingTestSender{}
	var hs store.Store = s
	if wrap != nil {
		hs = wrap(s)
	}
	h := handlers.NewNotificationChannelHandler(hs, sender)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			c := &auth.Claims{UserID: uuid.NewString(), OrgID: org.ID, Role: string(domain.RoleAdmin)}
			next.ServeHTTP(w, req.WithContext(middleware.SetClaims(req.Context(), c)))
		})
	})
	r.Post("/notification-channels/{id}/test", h.Test)

	create := func(typ, config string) *domain.NotificationChannel {
		t.Helper()
		ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: org.ID, Type: typ, Name: typ, Config: config, Enabled: true}
		if strings.Contains(typ, "_sms") || strings.Contains(typ, "_voice") {
			now := time.Now().UTC()
			ch.VerifiedAt = &now
		}
		if err := s.NotificationChannels().Create(ctx, ch); err != nil {
			t.Fatal(err)
		}
		return ch
	}
	run := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/notification-channels/"+id+"/test", nil))
		return w
	}
	return s, org, sender, create, run
}

func TestChannelTest_RateLimitedPerOrg(t *testing.T) {
	_, _, _, create, run := channelTestFixture(t, domain.PlanFree)
	ch := create("webhook", `{"url":"https://example.com/h"}`)
	for i := range 10 {
		if w := run(ch.ID); w.Code != http.StatusOK {
			t.Fatalf("test %d: status %d %s", i+1, w.Code, w.Body.String())
		}
	}
	// The cap is per org, not per channel.
	other := create("webhook", `{"url":"https://example.com/other"}`)
	w := run(other.ID)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th test: status %d, want 429", w.Code)
	}
	secs, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After = %q, want 1..60 seconds", w.Header().Get("Retry-After"))
	}
	// The SPA cannot read response headers, so the body carries the wait
	// and which Test buttons it covers.
	var body struct {
		RetryAfter int    `json:"retry_after"`
		LimitScope string `json:"limit_scope"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RetryAfter != secs || body.LimitScope != "org" {
		t.Fatalf("body retry_after=%d limit_scope=%q, want %d and org", body.RetryAfter, body.LimitScope, secs)
	}
}
