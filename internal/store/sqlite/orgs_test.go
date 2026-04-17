package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestOrgCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	org := &domain.Org{
		ID:        uuid.New().String(),
		Name:      "Acme",
		Slug:      "acme",
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}

	got, err := s.Orgs().GetByID(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Acme" || got.Slug != "acme" || got.Plan != domain.PlanFree {
		t.Fatalf("unexpected org: %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("created_at mismatch: got %v, want %v", got.CreatedAt, now)
	}
}

func TestOrgGetBySlug(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)

	got, err := s.Orgs().GetBySlug(context.Background(), org.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != org.ID {
		t.Fatalf("id mismatch: got %s, want %s", got.ID, org.ID)
	}
}

func TestOrgUpdate(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	org.Name = "Updated"
	org.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.Orgs().Update(ctx, org); err != nil {
		t.Fatal(err)
	}

	got, err := s.Orgs().GetByID(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Updated" {
		t.Fatalf("expected Updated, got %s", got.Name)
	}
}
