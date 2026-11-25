package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func fossStore(t *testing.T) store.Store {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fossOrg creates a free-plan org, which is what self-hosted installs use.
func fossOrg(t *testing.T, s store.Store) *domain.Org {
	t.Helper()
	org := &domain.Org{ID: uuid.New().String(), Name: "Self-hosted", Slug: "sh-" + uuid.New().String()[:8], Plan: domain.PlanFree}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return org
}

func withCaps(t *testing.T, c limits.Caps) {
	t.Helper()
	limits.Set(c)
	t.Cleanup(func() { limits.Set(limits.Default()) })
}

func postMonitor(t *testing.T, h *MonitorHandler, orgID string, interval int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":             "hb-" + uuid.New().String()[:6],
		"type":             "heartbeat",
		"interval_seconds": interval,
		"timeout_seconds":  5,
		"enabled":          true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", bytes.NewReader(body))
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: "u1", OrgID: orgID, Role: string(domain.RoleOwner)}))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	return rec
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b.Error
}

func assertOperatorMessage(t *testing.T, msg, env string) {
	t.Helper()
	low := strings.ToLower(msg)
	if strings.Contains(low, "upgrade") || strings.Contains(low, "plan") {
		t.Fatalf("self-hosted limit message must not mention plans or upgrades: %q", msg)
	}
	if !strings.Contains(msg, "operator") || !strings.Contains(msg, env) {
		t.Fatalf("message should say the operator set the limit and name %s: %q", env, msg)
	}
}

func TestFOSSMonitorCreate_NoCapAllowsMoreThanFive(t *testing.T) {
	withCaps(t, limits.Default())
	s := fossStore(t)
	org := fossOrg(t, s)
	h := NewMonitorHandler(s)
	for i := 0; i < 8; i++ {
		if rec := postMonitor(t, h, org.ID, 60); rec.Code != http.StatusCreated {
			t.Fatalf("monitor %d: status %d, body %s", i+1, rec.Code, rec.Body.String())
		}
	}
}

func TestFOSSMonitorCreate_OperatorCapRejectsThird(t *testing.T) {
	withCaps(t, limits.Caps{MaxMonitors: 2, MinCheckIntervalSeconds: limits.DefaultMinCheckIntervalSeconds})
	s := fossStore(t)
	org := fossOrg(t, s)
	h := NewMonitorHandler(s)
	for i := 0; i < 2; i++ {
		if rec := postMonitor(t, h, org.ID, 60); rec.Code != http.StatusCreated {
			t.Fatalf("monitor %d: status %d, body %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := postMonitor(t, h, org.ID, 60)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("third monitor: status %d, want 403", rec.Code)
	}
	msg := errorBody(t, rec)
	assertOperatorMessage(t, msg, limits.EnvMaxMonitors)
	if !strings.Contains(msg, "2 monitors") {
		t.Fatalf("message should state the cap: %q", msg)
	}
}

// The free-plan 300s floor must not apply on self-hosted installs.
func TestFOSSMonitorCreate_IntervalFloorIsOperatorSet(t *testing.T) {
	withCaps(t, limits.Default())
	s := fossStore(t)
	org := fossOrg(t, s)
	h := NewMonitorHandler(s)
	if rec := postMonitor(t, h, org.ID, 30); rec.Code != http.StatusCreated {
		t.Fatalf("30s interval: status %d, body %s", rec.Code, rec.Body.String())
	}

	withCaps(t, limits.Caps{MinCheckIntervalSeconds: 120})
	rec := postMonitor(t, h, org.ID, 60)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("60s under a 120s floor: status %d, want 403", rec.Code)
	}
	assertOperatorMessage(t, errorBody(t, rec), limits.EnvMinCheckInterval)
}

func TestFOSSChannelCap(t *testing.T) {
	ctx := context.Background()
	s := fossStore(t)
	org := fossOrg(t, s)
	addChannel := func() {
		ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "webhook", Name: "c", Config: "{}", Enabled: true}
		if err := s.NotificationChannels().Create(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}

	withCaps(t, limits.Default())
	for i := 0; i < 6; i++ {
		if err := checkChannelLimit(ctx, s, org.ID); err != nil {
			t.Fatalf("no cap, channel %d: %v", i+1, err)
		}
		addChannel()
	}

	withCaps(t, limits.Caps{MaxNotificationChannels: 6, MinCheckIntervalSeconds: 10})
	err := checkChannelLimit(ctx, s, org.ID)
	var capErr *limits.CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected CapError at the cap, got %v", err)
	}
	assertOperatorMessage(t, err.Error(), limits.EnvMaxNotificationChannels)
}

func TestFOSSEscalationPolicyCapAndLoops(t *testing.T) {
	ctx := context.Background()
	s := fossStore(t)
	org := fossOrg(t, s)

	if err := validateEscalationPolicy(ctx, s, org.ID, &domain.EscalationPolicy{Loop: true}); err != nil {
		t.Fatalf("looping policies must be allowed on self-hosted installs: %v", err)
	}

	withCaps(t, limits.Default())
	for i := 0; i < 3; i++ {
		if err := checkEscalationPolicyLimit(ctx, s, org.ID); err != nil {
			t.Fatalf("no cap, policy %d: %v", i+1, err)
		}
		if err := s.EscalationPolicies().Create(ctx, &domain.EscalationPolicy{ID: uuid.New().String(), OrgID: org.ID, Name: fmt.Sprintf("p%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	withCaps(t, limits.Caps{MaxEscalationPolicies: 3, MinCheckIntervalSeconds: 10})
	err := checkEscalationPolicyLimit(ctx, s, org.ID)
	if err == nil {
		t.Fatal("expected the cap to block a fourth policy")
	}
	assertOperatorMessage(t, err.Error(), limits.EnvMaxEscalationPolicies)
}

func TestFOSSMemberCap(t *testing.T) {
	ctx := context.Background()
	s := fossStore(t)
	org := fossOrg(t, s)
	for i := 0; i < 6; i++ {
		u := &domain.User{ID: uuid.New().String(), OrgID: org.ID, Email: fmt.Sprintf("u%d-%s@example.com", i, uuid.New().String()[:6]), Role: domain.RoleMember, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := s.Users().Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	withCaps(t, limits.Default())
	if err := checkMemberLimit(ctx, s, org); err != nil {
		t.Fatalf("no cap should allow more than 5 members: %v", err)
	}

	withCaps(t, limits.Caps{MaxMembers: 6, MinCheckIntervalSeconds: 10})
	err := checkMemberLimit(ctx, s, org)
	if err == nil {
		t.Fatal("expected the cap to block a seventh member")
	}
	assertOperatorMessage(t, err.Error(), limits.EnvMaxMembers)
}

func TestFOSSMetaExposesOperatorLimits(t *testing.T) {
	withCaps(t, limits.Caps{MaxMonitors: 25, MinCheckIntervalSeconds: 30})
	rec := httptest.NewRecorder()
	MetaGet(false, nil, "", false)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	var body struct {
		Edition        string      `json:"edition"`
		OperatorLimits limits.Caps `json:"operator_limits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Edition != "foss" {
		t.Fatalf("edition = %q", body.Edition)
	}
	want := limits.Caps{MaxMonitors: 25, MinCheckIntervalSeconds: 30}
	if body.OperatorLimits != want {
		t.Fatalf("operator_limits = %+v, want %+v", body.OperatorLimits, want)
	}
}

// The cap check probes the row at index cap-1 instead of loading every row.
// Check the edges: a cap of 1, and a cap above the store's default page size
// of 50, where an off-by-one or a dropped offset would let one extra through.
func TestFOSSCapBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, capN := range []int{1, 60} {
		t.Run(fmt.Sprintf("cap=%d", capN), func(t *testing.T) {
			s := fossStore(t)
			org := fossOrg(t, s)
			withCaps(t, limits.Caps{MaxNotificationChannels: capN, MinCheckIntervalSeconds: 10})
			for i := 0; i < capN; i++ {
				if err := checkChannelLimit(ctx, s, org.ID); err != nil {
					t.Fatalf("channel %d of %d: %v", i+1, capN, err)
				}
				ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "webhook", Name: fmt.Sprintf("c%03d", i), Config: "{}", Enabled: true}
				if err := s.NotificationChannels().Create(ctx, ch); err != nil {
					t.Fatal(err)
				}
			}
			var capErr *limits.CapError
			if err := checkChannelLimit(ctx, s, org.ID); !errors.As(err, &capErr) {
				t.Fatalf("at %d channels: expected CapError, got %v", capN, err)
			}

			// Another org is not affected by this org's count.
			other := fossOrg(t, s)
			if err := checkChannelLimit(ctx, s, other.ID); err != nil {
				t.Fatalf("other org: %v", err)
			}
		})
	}
}

// Disabled members do not hold a seat under the operator cap.
func TestFOSSMemberCapIgnoresDisabled(t *testing.T) {
	ctx := context.Background()
	s := fossStore(t)
	org := fossOrg(t, s)
	now := time.Now().UTC()
	u := &domain.User{ID: uuid.New().String(), OrgID: org.ID, Email: "gone-" + uuid.New().String()[:6] + "@example.com", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	withCaps(t, limits.Caps{MaxMembers: 1, MinCheckIntervalSeconds: 10})
	if err := checkMemberLimit(ctx, s, org); err == nil {
		t.Fatal("one active member should fill a cap of 1")
	}
	if err := s.Users().Disable(ctx, u.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := checkMemberLimit(ctx, s, org); err != nil {
		t.Fatalf("a disabled member should not count: %v", err)
	}
}
