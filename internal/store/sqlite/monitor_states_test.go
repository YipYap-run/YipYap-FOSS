package sqlite

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// Match rules must round-trip the header match fields, including the
// header_match_mode column added for equals/contains/regex matching.
func TestMatchRuleHeaderModeRoundTrip(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	st := &domain.MonitorState{
		ID:          uuid.New().String(),
		OrgID:       org.ID,
		Name:        "Redirecting",
		Slug:        "redirecting",
		HealthClass: "healthy",
	}
	if err := s.MonitorStates().Create(ctx, st); err != nil {
		t.Fatalf("create state: %v", err)
	}

	code := 302
	in := []*domain.MonitorMatchRule{{
		ID:              uuid.New().String(),
		MonitorID:       mon.ID,
		Position:        0,
		StatusCode:      &code,
		HeaderMatch:     "Location",
		HeaderValue:     "/login",
		HeaderMatchMode: "contains",
		StateID:         st.ID,
	}}
	if err := s.MonitorMatchRules().ReplaceForMonitor(ctx, mon.ID, in); err != nil {
		t.Fatalf("replace rules: %v", err)
	}

	got, err := s.MonitorMatchRules().ListByMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rules, want 1", len(got))
	}
	r := got[0]
	if r.HeaderMatch != "Location" || r.HeaderValue != "/login" || r.HeaderMatchMode != "contains" {
		t.Fatalf("header fields not round-tripped: name=%q value=%q mode=%q", r.HeaderMatch, r.HeaderValue, r.HeaderMatchMode)
	}
	if r.StatusCode == nil || *r.StatusCode != 302 {
		t.Fatalf("status_code not round-tripped: %v", r.StatusCode)
	}
}
