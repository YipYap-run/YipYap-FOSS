package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// mon-18: GET /monitors ran two extra queries per monitor (recent checks
// and labels). The count must not depend on how many monitors the org has.
func TestMonitorList_QueryCountIsFlat(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	addMonitors := func(n int) {
		for i := 0; i < n; i++ {
			m := &domain.Monitor{ID: uuid.New().String(), OrgID: alert.OrgID, Name: fmt.Sprintf("m%d", i), Type: domain.MonitorHTTP,
				Config: []byte(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10, Enabled: true,
				CreatedAt: now, UpdatedAt: now}
			if err := s.Monitors().Create(ctx, m); err != nil {
				t.Fatal(err)
			}
			if err := s.Monitors().SetLabels(ctx, m.ID, map[string]string{"env": "prod", "idx": fmt.Sprint(i)}); err != nil {
				t.Fatal(err)
			}
			for j := 0; j < 25; j++ {
				st := domain.StatusUp
				if j%5 == 0 {
					st = domain.StatusDown
				}
				addCheck(t, s, m.ID, st, now.Add(-time.Duration(j)*time.Minute))
			}
		}
	}

	type listed struct {
		ID           string            `json:"id"`
		Status       string            `json:"status"`
		Labels       map[string]string `json:"labels"`
		UptimePct    *float64          `json:"uptime_pct"`
		RecentChecks []struct {
			Status string `json:"status"`
		} `json:"recent_checks"`
	}
	run := func() (int64, []listed) {
		var n atomic.Int64
		observed := s.WithQueryObserver(func(string) { n.Add(1) })
		h := handlers.NewMonitorHandler(observed)
		rctx := middleware.SetClaims(context.Background(), &auth.Claims{UserID: user.ID, OrgID: alert.OrgID, Role: "member"})
		w := httptest.NewRecorder()
		h.List(w, httptest.NewRequest("GET", "/monitors?limit=100", nil).WithContext(rctx))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", w.Code, w.Body.String())
		}
		var out []listed
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return n.Load(), out
	}

	addMonitors(2)
	small, _ := run()
	addMonitors(20)
	large, out := run()
	if large != small {
		t.Fatalf("GET /monitors ran %d queries for 3 monitors and %d for 23; the count must not grow with monitors", small, large)
	}

	// The batched path still fills recent checks, uptime and labels.
	for _, m := range out {
		if m.ID == alert.MonitorID {
			continue // the fixture monitor has no checks or labels
		}
		if len(m.RecentChecks) != 20 || m.UptimePct == nil || *m.UptimePct != 80 {
			t.Fatalf("monitor %s: %d recent checks, uptime %v; want 20 and 80", m.ID, len(m.RecentChecks), m.UptimePct)
		}
		if m.RecentChecks[0].Status != "down" || m.Status != "down" {
			t.Fatalf("monitor %s: newest check should come first (down), got %q, status %q", m.ID, m.RecentChecks[0].Status, m.Status)
		}
		if m.Labels["env"] != "prod" || m.Labels["idx"] == "" {
			t.Fatalf("monitor %s labels %v", m.ID, m.Labels)
		}
	}
}

// ListRecentByMonitors keeps each monitor's newest checks, newest first,
// and caps them per monitor.
func TestCheckStore_ListRecentByMonitors(t *testing.T) {
	s, alert, _ := setupAlertHandlerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	for j := 0; j < 5; j++ {
		addCheck(t, s, alert.MonitorID, domain.StatusUp, now.Add(-time.Duration(j)*time.Minute))
	}
	got, err := s.Checks().ListRecentByMonitors(context.Background(), []string{alert.MonitorID, "missing"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	checks := got[alert.MonitorID]
	if len(checks) != 3 || len(got["missing"]) != 0 {
		t.Fatalf("got %d checks (want 3), %d for missing", len(checks), len(got["missing"]))
	}
	for i := range checks {
		if !checks[i].CheckedAt.Equal(now.Add(-time.Duration(i) * time.Minute)) {
			t.Fatalf("check %d at %v, want newest first", i, checks[i].CheckedAt)
		}
	}
}
