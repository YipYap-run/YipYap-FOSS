package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// countRows returns the number of rows in the given table.
func countRows(t *testing.T, s *SQLiteStore, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestOrgDeleteCleansUpOrphans verifies that deleting an org removes every
// org-scoped row, including child tables reached via ON DELETE CASCADE and
// the two join-only tables (alert_status_page_refs, cloudevent_ingest_idempotency).
func TestOrgDeleteCleansUpOrphans(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	org := createTestOrg(t, s)
	user := createTestUser(t, s, org.ID)
	mon := createTestMonitor(t, s, org.ID)
	alert := createTestAlert(t, s, org.ID, mon.ID)
	policy := createTestEscalationPolicy(t, s, org.ID)
	team := createTestTeam(t, s, org.ID)

	now := time.Now().UTC().Format(timeFormat)

	// Child tables (cascade from their parents).
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("insert failed (%s): %v", q, err)
		}
	}
	exec(`INSERT INTO alert_events (id, alert_id, event_type, created_at) VALUES (?, ?, ?, ?)`,
		uuid.New().String(), alert.ID, "fired", now)
	exec(`INSERT INTO alert_escalation_state (alert_id, current_step_id, retry_count, step_entered_at) VALUES (?, ?, 0, ?)`,
		alert.ID, "step", now)
	exec(`INSERT INTO escalation_steps (id, policy_id, position) VALUES (?, ?, 0)`,
		uuid.New().String(), policy.ID)
	exec(`INSERT INTO team_members (team_id, user_id) VALUES (?, ?)`, team.ID, user.ID)
	exec(`INSERT INTO schedules (id, team_id, effective_from) VALUES (?, ?, ?)`,
		uuid.New().String(), team.ID, now)
	exec(`INSERT INTO monitor_labels (monitor_id, key, value) VALUES (?, 'k', 'v')`, mon.ID)
	exec(`INSERT INTO monitor_checks (id, monitor_id, status, checked_at) VALUES (?, ?, 'up', ?)`,
		uuid.New().String(), mon.ID, now)

	// Join-only tables (no org_id, no FK).
	exec(`INSERT INTO alert_status_page_refs (id, alert_id, provider, updated_at) VALUES (?, ?, ?, ?)`,
		uuid.New().String(), alert.ID, "statuspage", now)
	exec(`INSERT INTO cloudevent_ingest_idempotency (monitor_id, event_id) VALUES (?, ?)`,
		mon.ID, "evt-1")

	// Newer org_id-bearing tables.
	exec(`INSERT INTO services (id, org_id, name, slug) VALUES (?, ?, 'svc', 'svc')`,
		uuid.New().String(), org.ID)
	exec(`INSERT INTO billing (org_id) VALUES (?)`, org.ID)
	exec(`INSERT INTO sms_voice_usage (org_id, period_key, sms_used) VALUES (?, 'm:2026-11', 3)`, org.ID)
	exec(`INSERT INTO sms_voice_usage_warnings (org_id, period_key, kind, threshold, sent_at) VALUES (?, 'm:2026-11', 'sms', 80, ?)`, org.ID, now)
	exec(`INSERT INTO support_tickets (id, org_id, user_id, subject, created_at, updated_at) VALUES (?, ?, ?, 'subj', ?, ?)`,
		uuid.New().String(), org.ID, user.ID, now, now)
	exec(`INSERT INTO incidents (id, org_id, title, started_at, created_by, created_at, updated_at) VALUES (?, ?, 'inc', ?, ?, ?, ?)`,
		uuid.New().String(), org.ID, now, user.ID, now, now)
	exec(`INSERT INTO status_pages (id, org_id, slug, name) VALUES (?, ?, 'status', 'Status')`,
		uuid.New().String(), org.ID)
	exec(`INSERT INTO monitor_groups (id, org_id, name, created_at, updated_at) VALUES (?, ?, 'grp', ?, ?)`,
		uuid.New().String(), org.ID, now, now)

	if err := s.Orgs().Delete(ctx, org.ID); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"orgs", "users", "monitors", "alerts", "escalation_policies", "teams",
		"alert_events", "alert_escalation_state", "escalation_steps",
		"team_members", "schedules", "monitor_labels", "monitor_checks",
		"alert_status_page_refs", "cloudevent_ingest_idempotency",
		"services", "billing", "support_tickets", "incidents", "status_pages",
		"monitor_groups", "sms_voice_usage", "sms_voice_usage_warnings",
	} {
		if n := countRows(t, s, table); n != 0 {
			t.Errorf("table %s still has %d rows after org delete", table, n)
		}
	}
}
