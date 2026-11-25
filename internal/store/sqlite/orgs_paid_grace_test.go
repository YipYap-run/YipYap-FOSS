package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/google/uuid"
)

// Plan names as stored. The Pro and Promo constants exist only in the
// SaaS build, and this store logic runs in both.
const (
	planPro   domain.OrgPlan = "pro"
	planPromo domain.OrgPlan = "promo"
)

func setPlan(t *testing.T, s *SQLiteStore, org *domain.Org, plan domain.OrgPlan) *domain.Org {
	t.Helper()
	ctx := context.Background()
	org.Plan = plan
	if err := s.Orgs().Update(ctx, org); err != nil {
		t.Fatal(err)
	}
	got, err := s.Orgs().GetByID(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOrgUpdateTracksPaidGrace(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)
	if org.PaidGraceUntil != nil {
		t.Fatal("new org has a paid grace deadline")
	}

	// Free to Free (never paid): no grace.
	org = setPlan(t, s, org, domain.PlanFree)
	if org.PaidGraceUntil != nil {
		t.Fatalf("free org that never paid got grace %v", org.PaidGraceUntil)
	}

	// Pro to Free starts the default grace.
	org = setPlan(t, s, org, planPro)
	if org.PaidGraceUntil != nil {
		t.Fatal("pro org has a grace deadline")
	}
	before := time.Now().UTC()
	org = setPlan(t, s, org, domain.PlanFree)
	if org.PaidGraceUntil == nil {
		t.Fatal("downgrade did not set a grace deadline")
	}
	want := before.Add(domain.DefaultPaidGraceDays * 24 * time.Hour)
	if d := org.PaidGraceUntil.Sub(want); d < -time.Minute || d > time.Minute {
		t.Fatalf("grace until %v, want about %v", org.PaidGraceUntil, want)
	}

	// Staying on Free (any other edit) keeps the deadline.
	first := *org.PaidGraceUntil
	org.Name = "Renamed"
	org.PaidGraceUntil = nil
	org = setPlan(t, s, org, domain.PlanFree)
	if org.PaidGraceUntil == nil || !org.PaidGraceUntil.Equal(first) {
		t.Fatalf("edit on a free org moved the deadline: %v, want %v", org.PaidGraceUntil, first)
	}

	// An upgrade clears it.
	org = setPlan(t, s, org, planPro)
	if org.PaidGraceUntil != nil {
		t.Fatal("upgrade kept the grace deadline")
	}

	// Promo to Free with a caller-supplied deadline (the promo expiry
	// middleware) uses that deadline instead of a fresh grace.
	org = setPlan(t, s, org, planPromo)
	explicit := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	org.PaidGraceUntil = &explicit
	org = setPlan(t, s, org, domain.PlanFree)
	if org.PaidGraceUntil == nil || !org.PaidGraceUntil.Equal(explicit) {
		t.Fatalf("promo downgrade deadline %v, want %v", org.PaidGraceUntil, explicit)
	}
}

func TestOrgPaidGraceUsesPromoGraceDays(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)
	org = setPlan(t, s, org, planPro)
	org.PromoGraceDays = 7
	before := time.Now().UTC()
	org = setPlan(t, s, org, domain.PlanFree)
	want := before.Add(7 * 24 * time.Hour)
	if org.PaidGraceUntil == nil || org.PaidGraceUntil.Sub(want).Abs() > time.Minute {
		t.Fatalf("grace until %v, want about %v", org.PaidGraceUntil, want)
	}
}

// The paid-grace backfill gives Free orgs that already hold paid channels
// the default grace, and leaves other orgs alone.
func TestPaidGraceBackfill(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	withPaid := createTestOrg(t, s)
	withFreeOnly := createTestOrg(t, s)
	for _, c := range []struct{ org, typ string }{{withPaid.ID, "telnyx_sms"}, {withFreeOnly.ID, "webhook"}} {
		ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: c.org, Type: c.typ, Name: c.typ, Config: `{}`, Enabled: true}
		if err := s.NotificationChannels().Create(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := migrationsFS.ReadFile("migrations/068_org_paid_grace.sql")
	if err != nil {
		t.Fatal(err)
	}
	update := string(raw)[strings.Index(string(raw), "UPDATE orgs"):]
	if _, err := s.db.ExecContext(ctx, update); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Orgs().GetByID(ctx, withPaid.ID)
	want := time.Now().UTC().Add(30 * 24 * time.Hour)
	if got.PaidGraceUntil == nil || got.PaidGraceUntil.Sub(want).Abs() > time.Minute {
		t.Fatalf("backfilled grace %v, want about %v", got.PaidGraceUntil, want)
	}
	got, _ = s.Orgs().GetByID(ctx, withFreeOnly.ID)
	if got.PaidGraceUntil != nil {
		t.Fatal("org without paid channels got a grace deadline")
	}
}
