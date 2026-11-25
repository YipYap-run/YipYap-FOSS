package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestTelegramLinkCodeClaimIsSingleUseAndExpires(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	codes := s.TelegramLinkCodes()

	c := &domain.TelegramLinkCode{
		ID: uuid.New().String(), CodeHash: "hash-1", OrgID: org.ID, CreatedBy: "u1",
		Purpose: domain.TelegramLinkPurposeChannel, ChannelName: "Ops",
		ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now,
	}
	if err := codes.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := codes.GetByHash(ctx, "hash-1")
	if err != nil || got == nil || got.ID != c.ID || got.SubjectID != "" || got.ChannelName != "Ops" || got.UsedAt != nil {
		t.Fatalf("GetByHash = %+v, %v", got, err)
	}
	if missing, err := codes.GetByHash(ctx, "nope"); err != nil || missing != nil {
		t.Fatalf("missing hash = %+v, %v", missing, err)
	}

	// Expired at claim time.
	if ok, err := codes.Claim(ctx, c.ID, now.Add(16*time.Minute)); err != nil || ok {
		t.Fatalf("claim after expiry = %v, %v", ok, err)
	}
	if ok, err := codes.Claim(ctx, c.ID, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if ok, err := codes.Claim(ctx, c.ID, now.Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("second claim = %v, %v", ok, err)
	}
	if err := codes.SetLinked(ctx, c.ID, "-100123", "Ops room", "ch-1"); err != nil {
		t.Fatal(err)
	}
	got, _ = codes.GetByID(ctx, c.ID)
	if got.UsedAt == nil || got.LinkedChatID != "-100123" || got.LinkedChatTitle != "Ops room" || got.LinkedSubjectID != "ch-1" {
		t.Fatalf("linked row = %+v", got)
	}
	if n, err := codes.CountByOrgSince(ctx, org.ID, now.Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	if err := codes.Create(ctx, &domain.TelegramLinkCode{ID: uuid.New().String(), CodeHash: "hash-1", OrgID: org.ID,
		CreatedBy: "u1", Purpose: domain.TelegramLinkPurposeChannel, ExpiresAt: now, CreatedAt: now}); err == nil {
		t.Fatal("duplicate code hash accepted")
	}
}
