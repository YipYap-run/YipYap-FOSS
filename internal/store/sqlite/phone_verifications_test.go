package sqlite

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestPhoneChannelMigrationsApplyInOrder(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	var createdBy, verifiedAt int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('phone_verifications') WHERE name = 'created_by'`).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('notification_channels') WHERE name = 'verified_at'`).Scan(&verifiedAt); err != nil {
		t.Fatal(err)
	}
	if createdBy != 1 || verifiedAt != 1 {
		t.Fatalf("created_by = %d, verified_at = %d, want 1 and 1", createdBy, verifiedAt)
	}

	for _, table := range []string{"slack_installations", "discord_installations", "telegram_link_codes"} {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("table %s missing after a fresh migrate", table)
		}
	}

	org := createTestOrg(t, s)
	now := time.Now().UTC().Truncate(time.Second)
	for i, actor := range []string{"user-a", "user-a", "user-b"} {
		v := &domain.PhoneVerification{ID: uuid.New().String(), OrgID: org.ID, SubjectType: domain.PhoneSubjectChannel,
			SubjectID: "ch", Phone: "+15555550123", Method: "sms", CreatedBy: actor, CodeHash: "h",
			ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if err := s.PhoneVerifications().Create(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PhoneVerifications().CountByActorSince(ctx, org.ID, "user-a", now.Add(-time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("actor count = %d, err %v, want 2", n, err)
	}
}

func TestPhoneBudgetReserveConcurrentAtCap(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	w := store.BudgetWindow{Key: "phone:hour:+15555550123:1700000000", Limit: 5, WindowEnd: time.Now().Add(time.Hour)}
	for i := 0; i < w.Limit-1; i++ {
		if ok, err := s.PhoneVerifications().ReserveWindow(ctx, w); err != nil || !ok {
			t.Fatalf("fill %d: ok=%v err=%v", i, ok, err)
		}
	}

	const n = 8
	results := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := s.PhoneVerifications().ReserveWindow(ctx, w)
			results[i] = err == nil && ok
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent reservations succeeded %d times, want 1", successes)
	}

	if ok, err := s.PhoneVerifications().ReserveWindow(ctx, w); err != nil || ok {
		t.Fatalf("post-burst reservation ok=%v err=%v, want denied", ok, err)
	}
}

func TestPhoneBudgetCooldownConcurrent(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	const n = 8
	results := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := s.PhoneVerifications().ReserveCooldown(ctx, "cooldown:channel:ch1", now, now.Add(time.Minute))
			results[i] = err == nil && ok
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent cooldown reservations succeeded %d times, want 1", successes)
	}

	if ok, err := s.PhoneVerifications().ReserveCooldown(ctx, "cooldown:channel:ch1", now, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("cooldown within window ok=%v err=%v, want denied", ok, err)
	}
	if ok, err := s.PhoneVerifications().ReserveCooldown(ctx, "cooldown:channel:ch1", now.Add(time.Minute), now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("cooldown after window ok=%v err=%v, want allowed", ok, err)
	}
}

func TestChannelVerifiedAtRoundTrip(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "telnyx_sms", Name: "p", Config: `{"to":"+15555550123"}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	stale, err := s.NotificationChannels().GetByID(ctx, ch.ID)
	if err != nil || stale.VerifiedAt != nil {
		t.Fatalf("new channel verified_at = %v, err %v", stale.VerifiedAt, err)
	}
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	if err := s.NotificationChannels().SetVerifiedAt(ctx, ch.ID, &at); err != nil {
		t.Fatal(err)
	}
	// Update must not touch verified_at, even from a stale struct.
	stale.Name = "renamed"
	if err := s.NotificationChannels().Update(ctx, stale); err != nil {
		t.Fatal(err)
	}
	list, err := s.NotificationChannels().ListByOrg(ctx, org.ID, store.ListParams{})
	if err != nil || len(list) != 1 || list[0].VerifiedAt == nil || !list[0].VerifiedAt.Equal(at) {
		t.Fatalf("list verified_at: %+v err %v", list, err)
	}
	if err := s.NotificationChannels().SetVerifiedAt(ctx, ch.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.NotificationChannels().GetByID(ctx, ch.ID)
	if got.VerifiedAt != nil {
		t.Fatal("verified_at not cleared")
	}
}

func TestPhoneVerificationRowsGoWithChannelAndOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "telnyx_sms", Name: "p", Config: `{"to":"+15555550123"}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	v := &domain.PhoneVerification{ID: uuid.New().String(), OrgID: org.ID, SubjectType: domain.PhoneSubjectChannel, SubjectID: ch.ID,
		Phone: "+15555550123", Method: "sms", CodeHash: "h", ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now}
	if err := s.PhoneVerifications().Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.NotificationChannels().Delete(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "phone_verifications"); n != 0 {
		t.Fatalf("channel delete left %d verification rows", n)
	}

	v.ID = uuid.New().String()
	v.SubjectType = domain.PhoneSubjectContactMethod
	if err := s.PhoneVerifications().Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.Orgs().Delete(ctx, org.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "phone_verifications"); n != 0 {
		t.Fatalf("org delete left %d verification rows", n)
	}
}

func TestMigrationGrandfathersExistingPhoneChannels(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	// Simulate a pre-044 row: insert, clear verified_at, then rerun the
	// grandfather statement from the migration.
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "telnyx_voice", Name: "p", Config: `{"to":"+15555550123"}`, Enabled: true}
	hook := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "webhook", Name: "w", Config: `{}`, Enabled: true}
	for _, c := range []*domain.NotificationChannel{ch, hook} {
		if err := s.NotificationChannels().Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := migrationsFS.ReadFile("migrations/071_phone_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(raw)
	idx := strings.LastIndex(sqlText, "UPDATE notification_channels")
	if idx < 0 {
		t.Fatal("grandfather UPDATE missing from migration")
	}
	if _, err := s.db.ExecContext(ctx, sqlText[idx:]); err != nil {
		t.Fatal(err)
	}
	got, _ := s.NotificationChannels().GetByID(ctx, ch.ID)
	if got.VerifiedAt == nil {
		t.Fatal("existing phone channel was not grandfathered")
	}
	other, _ := s.NotificationChannels().GetByID(ctx, hook.ID)
	if other.VerifiedAt != nil {
		t.Fatal("non-phone channel should not be marked verified")
	}
}
