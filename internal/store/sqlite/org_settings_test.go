package sqlite

import (
	"context"
	"strings"
	"testing"
)

func TestOrgSettingsSetAndGet(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)
	ctx := context.Background()

	if err := s.OrgSettings().Set(ctx, org.ID, "otel_endpoint", "https://otel.example.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := s.OrgSettings().Get(ctx, org.ID, "otel_endpoint")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "https://otel.example.com" {
		t.Errorf("Get = %q, want %q", got, "https://otel.example.com")
	}
}

func TestOrgSettingsSetOverwrite(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)
	ctx := context.Background()

	if err := s.OrgSettings().Set(ctx, org.ID, "otel_endpoint", "https://old.example.com"); err != nil {
		t.Fatalf("Set (initial): %v", err)
	}
	if err := s.OrgSettings().Set(ctx, org.ID, "otel_endpoint", "https://new.example.com"); err != nil {
		t.Fatalf("Set (overwrite): %v", err)
	}

	got, err := s.OrgSettings().Get(ctx, org.ID, "otel_endpoint")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "https://new.example.com" {
		t.Errorf("Get = %q, want %q", got, "https://new.example.com")
	}
}

func TestOrgSettingsDelete(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)
	ctx := context.Background()

	if err := s.OrgSettings().Set(ctx, org.ID, "otel_endpoint", "https://otel.example.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.OrgSettings().Delete(ctx, org.ID, "otel_endpoint"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.OrgSettings().Get(ctx, org.ID, "otel_endpoint")
	if err == nil {
		t.Fatal("expected error after Delete, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestOrgSettingsGetAll(t *testing.T) {
	s := setupTestDB(t)
	org := createTestOrg(t, s)
	ctx := context.Background()

	if err := s.OrgSettings().Set(ctx, org.ID, "otel_endpoint", "https://otel.example.com"); err != nil {
		t.Fatalf("Set endpoint: %v", err)
	}
	if err := s.OrgSettings().Set(ctx, org.ID, "otel_headers", "Authorization=Bearer token"); err != nil {
		t.Fatalf("Set headers: %v", err)
	}

	all, err := s.OrgSettings().GetAll(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAll returned %d entries, want 2", len(all))
	}
	if all["otel_endpoint"] != "https://otel.example.com" {
		t.Errorf("otel_endpoint = %q, want %q", all["otel_endpoint"], "https://otel.example.com")
	}
	if all["otel_headers"] != "Authorization=Bearer token" {
		t.Errorf("otel_headers = %q, want %q", all["otel_headers"], "Authorization=Bearer token")
	}
}

func TestOrgSettingsGetAllIsolation(t *testing.T) {
	s := setupTestDB(t)
	org1 := createTestOrg(t, s)
	org2 := createTestOrg(t, s)
	ctx := context.Background()

	if err := s.OrgSettings().Set(ctx, org1.ID, "otel_endpoint", "https://org1.example.com"); err != nil {
		t.Fatalf("Set org1: %v", err)
	}

	all, err := s.OrgSettings().GetAll(ctx, org2.ID)
	if err != nil {
		t.Fatalf("GetAll org2: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("GetAll for different org returned %d entries, want 0", len(all))
	}
}
