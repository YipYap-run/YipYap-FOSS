package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func newContactMethod(userID, orgID string, typ domain.ContactMethodType, cfg string, verified bool) *domain.ContactMethod {
	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.ContactMethod{
		ID: uuid.New().String(), OrgID: orgID, UserID: userID, Type: typ,
		Label: string(typ), Config: cfg, CreatedAt: now, UpdatedAt: now,
	}
	if verified {
		m.VerifiedAt = &now
	}
	return m
}

func TestContactMethodsCRUDScopedToUser(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	alice := createTestUser(t, s, org.ID)
	bob := createTestUser(t, s, org.ID)

	m := newContactMethod(alice.ID, org.ID, domain.ContactSMS, `{"phone":"+15005550006"}`, false)
	if err := s.ContactMethods().Create(ctx, m); err != nil {
		t.Fatal(err)
	}

	got, err := s.ContactMethods().Get(ctx, alice.ID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != domain.ContactSMS || got.Config != m.Config || got.Verified() {
		t.Fatalf("unexpected method: %+v", got)
	}

	// Another user cannot read, update or delete it.
	if _, err := s.ContactMethods().Get(ctx, bob.ID, m.ID); !errors.Is(err, store.ErrContactMethodNotFound) {
		t.Fatalf("cross-user get: want not found, got %v", err)
	}
	stolen := *got
	stolen.UserID = bob.ID
	stolen.Label = "mine now"
	if err := s.ContactMethods().Update(ctx, &stolen, false); !errors.Is(err, store.ErrContactMethodNotFound) {
		t.Fatalf("cross-user update: want not found, got %v", err)
	}
	if err := s.ContactMethods().Delete(ctx, bob.ID, m.ID); !errors.Is(err, store.ErrContactMethodNotFound) {
		t.Fatalf("cross-user delete: want not found, got %v", err)
	}

	// Verify, then a config change resets verification.
	if err := s.ContactMethods().MarkVerified(ctx, alice.ID, m.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ContactMethods().Get(ctx, alice.ID, m.ID)
	if !got.Verified() {
		t.Fatal("expected verified")
	}
	got.Config = `{"phone":"+15005550007"}`
	if err := s.ContactMethods().Update(ctx, got, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ContactMethods().Get(ctx, alice.ID, m.ID)
	if got.Verified() || got.Config != `{"phone":"+15005550007"}` {
		t.Fatalf("expected unverified with new config, got %+v", got)
	}

	n, err := s.ContactMethods().CountByUser(ctx, alice.ID)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	if err := s.ContactMethods().Delete(ctx, alice.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ContactMethods().CountByUser(ctx, alice.ID); n != 0 {
		t.Fatalf("count after delete = %d", n)
	}
}

func TestContactMethodVerificationState(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	u := createTestUser(t, s, org.ID)
	m := newContactMethod(u.ID, org.ID, domain.ContactEmail, `{"address":"x@example.com"}`, false)
	if err := s.ContactMethods().Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	sent := time.Now().UTC().Truncate(time.Second)
	if err := s.ContactMethods().SetVerificationCode(ctx, u.ID, m.ID, "hash", exp, sent); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		n, err := s.ContactMethods().IncrementVerifyAttempts(ctx, u.ID, m.ID)
		if err != nil || n != want {
			t.Fatalf("attempts = %d (%v), want %d", n, err, want)
		}
	}
	got, _ := s.ContactMethods().Get(ctx, u.ID, m.ID)
	if got.VerifyCodeHash != "hash" || got.VerifyExpiresAt == nil || !got.VerifyExpiresAt.Equal(exp) ||
		got.LastSentAt == nil || got.VerifyAttempts != 3 {
		t.Fatalf("unexpected verification state: %+v", got)
	}
	// A new code resets the attempt counter.
	if err := s.ContactMethods().SetVerificationCode(ctx, u.ID, m.ID, "hash2", exp, sent); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ContactMethods().Get(ctx, u.ID, m.ID)
	if got.VerifyAttempts != 0 {
		t.Fatalf("attempts not reset: %d", got.VerifyAttempts)
	}
	if err := s.ContactMethods().MarkVerified(ctx, u.ID, m.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ContactMethods().Get(ctx, u.ID, m.ID)
	if !got.Verified() || got.VerifyCodeHash != "" || got.VerifyExpiresAt != nil {
		t.Fatalf("verification not finalized: %+v", got)
	}
}

func TestNotificationRulesScopedAndPagingExcludesUnverified(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	u := createTestUser(t, s, org.ID)
	other := createTestUser(t, s, org.ID)

	verified := newContactMethod(u.ID, org.ID, domain.ContactSMS, `{"phone":"+15005550006"}`, true)
	unverified := newContactMethod(u.ID, org.ID, domain.ContactVoice, `{"phone":"+15005550006"}`, false)
	foreign := newContactMethod(other.ID, org.ID, domain.ContactSMS, `{"phone":"+15005550009"}`, true)
	for _, m := range []*domain.ContactMethod{verified, unverified, foreign} {
		if err := s.ContactMethods().Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	rule := func(methodID string, delay int) *domain.NotificationRule {
		return &domain.NotificationRule{ID: uuid.New().String(), OrgID: org.ID, UserID: u.ID,
			ContactMethodID: methodID, DelayMinutes: delay, Urgency: domain.UrgencyAny, CreatedAt: now}
	}
	later := rule(verified.ID, 5)
	first := rule(verified.ID, 0)
	onUnverified := rule(unverified.ID, 1)
	for _, r := range []*domain.NotificationRule{later, first, onUnverified} {
		if err := s.NotificationRules().Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// A rule cannot point at someone else's method.
	if err := s.NotificationRules().Create(ctx, rule(foreign.ID, 0)); !errors.Is(err, store.ErrContactMethodNotFound) {
		t.Fatalf("rule on foreign method: want not found, got %v", err)
	}
	moved := *first
	moved.ContactMethodID = foreign.ID
	if err := s.NotificationRules().Update(ctx, &moved); !errors.Is(err, store.ErrNotificationRuleNotFound) {
		t.Fatalf("rule moved to foreign method: want not found, got %v", err)
	}
	// Another user cannot read or delete the rule.
	if _, err := s.NotificationRules().Get(ctx, other.ID, first.ID); !errors.Is(err, store.ErrNotificationRuleNotFound) {
		t.Fatalf("cross-user rule get: %v", err)
	}
	if err := s.NotificationRules().Delete(ctx, other.ID, first.ID); !errors.Is(err, store.ErrNotificationRuleNotFound) {
		t.Fatalf("cross-user rule delete: %v", err)
	}

	rules, err := s.NotificationRules().ListByUser(ctx, u.ID)
	if err != nil || len(rules) != 3 || rules[0].ID != first.ID {
		t.Fatalf("rules = %+v, %v", rules, err)
	}

	targets, err := s.NotificationRules().ListPagingTargets(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("paging targets = %d, want 2 (unverified excluded)", len(targets))
	}
	if targets[0].Rule.ID != first.ID || targets[1].Rule.ID != later.ID {
		t.Fatalf("targets not ordered by delay: %s, %s", targets[0].Rule.ID, targets[1].Rule.ID)
	}
	for _, tg := range targets {
		if tg.Method.ID != verified.ID || !tg.Method.Verified() {
			t.Fatalf("unexpected target method %+v", tg.Method)
		}
	}

	// Deleting a method removes its rules.
	if err := s.ContactMethods().Delete(ctx, u.ID, verified.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.NotificationRules().CountByUser(ctx, u.ID); n != 1 {
		t.Fatalf("rules left after method delete = %d, want 1", n)
	}
}

func TestMarkEmailVerifiedSeedsDefaultEmailMethod(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	u := createTestUser(t, s, org.ID)

	if ms, _ := s.ContactMethods().ListByUser(ctx, u.ID); len(ms) != 0 {
		t.Fatalf("unverified user already has %d methods", len(ms))
	}
	if err := s.Users().MarkEmailVerified(ctx, u.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Calling it again does not duplicate the method.
	if err := s.Users().MarkEmailVerified(ctx, u.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	ms, err := s.ContactMethods().ListByUser(ctx, u.ID)
	if err != nil || len(ms) != 1 {
		t.Fatalf("methods = %d, %v", len(ms), err)
	}
	if ms[0].Type != domain.ContactEmail || !ms[0].Verified() || ms[0].Config != `{"address":"`+u.Email+`"}` {
		t.Fatalf("unexpected seeded method %+v", ms[0])
	}
	targets, err := s.NotificationRules().ListPagingTargets(ctx, u.ID)
	if err != nil || len(targets) != 1 || targets[0].Rule.DelayMinutes != 0 || targets[0].Rule.Urgency != domain.UrgencyAny {
		t.Fatalf("seeded rule = %+v, %v", targets, err)
	}
}

func TestContactMethodsRemovedWithUser(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	u := createTestUser(t, s, org.ID)
	if err := s.Users().MarkEmailVerified(ctx, u.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Users().Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_contact_methods WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("contact methods left after user delete: %d", n)
	}
}

// The 042 backfill gives existing verified users their email method, and
// running it twice does not duplicate anything.
func TestContactMethodBackfillMigration(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	u := createTestUser(t, s, org.ID)
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET email_verified_at = ? WHERE id = ?`, timeNowUTC(), u.ID); err != nil {
		t.Fatal(err)
	}
	data, err := migrationsFS.ReadFile("migrations/060_user_contact_methods.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		for _, stmt := range store.SplitSQLStatements(string(data)) {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("run %d: %v", run, err)
			}
		}
	}
	targets, err := s.NotificationRules().ListPagingTargets(ctx, u.ID)
	if err != nil || len(targets) != 1 || targets[0].Method.Type != domain.ContactEmail || targets[0].Method.Label != "Account email" {
		t.Fatalf("backfill targets = %+v, %v", targets, err)
	}
}

func TestContactMethodSendBudget(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	alice := createTestUser(t, s, org.ID)
	bob := createTestUser(t, s, org.ID)
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		ok, err := s.ContactMethods().ReserveSendBudget(ctx, alice.ID, org.ID, now, false, 3, 100)
		if err != nil || !ok {
			t.Fatalf("reserve %d = %v, %v", i, ok, err)
		}
	}
	if ok, err := s.ContactMethods().ReserveSendBudget(ctx, alice.ID, org.ID, now, false, 3, 100); err != nil || ok {
		t.Fatalf("over-budget reserve = %v, %v", ok, err)
	}
	if ok, err := s.ContactMethods().ReserveSendBudget(ctx, bob.ID, org.ID, now, false, 3, 100); err != nil || !ok {
		t.Fatalf("other user reserve = %v, %v", ok, err)
	}

	nextDay := now.AddDate(0, 0, 1)
	if ok, err := s.ContactMethods().ReserveSendBudget(ctx, alice.ID, org.ID, nextDay, false, 3, 100); err != nil || !ok {
		t.Fatalf("next-day reserve = %v, %v", ok, err)
	}

	if ok, err := s.ContactMethods().ReserveSendBudget(ctx, alice.ID, org.ID, now, true, 100, 2); err != nil || !ok {
		t.Fatalf("org sms 1 = %v, %v", ok, err)
	}
	if ok, err := s.ContactMethods().ReserveSendBudget(ctx, bob.ID, org.ID, now, true, 100, 2); err != nil || !ok {
		t.Fatalf("org sms 2 = %v, %v", ok, err)
	}
	if ok, err := s.ContactMethods().ReserveSendBudget(ctx, bob.ID, org.ID, now, true, 100, 2); err != nil || ok {
		t.Fatalf("org sms over budget = %v, %v", ok, err)
	}
}
