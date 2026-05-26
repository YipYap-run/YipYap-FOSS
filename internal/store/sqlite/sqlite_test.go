package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestMigrate(t *testing.T) {
	s := setupTestDB(t)
	// Running migrate again should be idempotent.
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionCommit(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	orgID := uuid.New().String()

	err := s.Tx(ctx, func(tx store.Store) error {
		return tx.Orgs().Create(ctx, &domain.Org{
			ID:        orgID,
			Name:      "Tx Org",
			Slug:      "tx-org",
			Plan:      domain.PlanFree,
			CreatedAt: now,
			UpdatedAt: now,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should be visible after commit.
	got, err := s.Orgs().GetByID(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Tx Org" {
		t.Fatalf("expected Tx Org, got %s", got.Name)
	}
}

func TestTransactionRollback(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	orgID := uuid.New().String()

	err := s.Tx(ctx, func(tx store.Store) error {
		_ = tx.Orgs().Create(ctx, &domain.Org{
			ID:        orgID,
			Name:      "Rollback Org",
			Slug:      "rollback-org",
			Plan:      domain.PlanFree,
			CreatedAt: now,
			UpdatedAt: now,
		})
		return fmt.Errorf("rollback")
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// Should not be visible after rollback.
	_, err = s.Orgs().GetByID(ctx, orgID)
	if err == nil {
		t.Fatal("expected org to not exist after rollback")
	}
}
