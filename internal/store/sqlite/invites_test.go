package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestInviteStoreLifecycle(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	now := time.Now().UTC().Truncate(time.Second)

	inv := &domain.OrgInvite{
		ID: uuid.New().String(), OrgID: org.ID, Email: "New@Example.com", Role: domain.RoleViewer,
		TokenHash: "hash-1", InvitedBy: "u1", ExpiresAt: now.Add(domain.InviteTTL), CreatedAt: now,
	}
	if err := s.Invites().Create(ctx, inv); err != nil {
		t.Fatal(err)
	}
	dup := *inv
	dup.ID = uuid.New().String()
	dup.Email = "new@example.com"
	if err := s.Invites().Create(ctx, &dup); err == nil {
		t.Fatal("second pending invite for the same email (other case) was allowed")
	}
	got, err := s.Invites().GetPendingByEmail(ctx, org.ID, "NEW@example.com")
	if err != nil || got.ID != inv.ID || got.Role != domain.RoleViewer || !got.ExpiresAt.Equal(inv.ExpiresAt) {
		t.Fatalf("GetPendingByEmail = %+v, %v", got, err)
	}
	if n, err := s.Invites().CountActive(ctx, org.ID, now); err != nil || n != 1 {
		t.Fatalf("CountActive = %d, %v", n, err)
	}
	if n, _ := s.Invites().CountActive(ctx, org.ID, now.Add(domain.InviteTTL+time.Hour)); n != 0 {
		t.Fatalf("CountActive after expiry = %d", n)
	}

	// Accepting with a stale hash or after expiry claims nothing.
	if ok, _ := s.Invites().MarkAccepted(ctx, inv.ID, "wrong", now); ok {
		t.Fatal("accepted with the wrong hash")
	}
	if ok, _ := s.Invites().MarkAccepted(ctx, inv.ID, "hash-1", now.Add(domain.InviteTTL+time.Second)); ok {
		t.Fatal("accepted after expiry")
	}
	newExp := now.Add(2 * domain.InviteTTL)
	if err := s.Invites().Rotate(ctx, inv.ID, "hash-2", newExp); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Invites().MarkAccepted(ctx, inv.ID, "hash-1", now); ok {
		t.Fatal("accepted with the pre-rotation hash")
	}
	if ok, err := s.Invites().MarkAccepted(ctx, inv.ID, "hash-2", now); err != nil || !ok {
		t.Fatalf("MarkAccepted = %v, %v", ok, err)
	}
	if ok, _ := s.Invites().MarkAccepted(ctx, inv.ID, "hash-2", now); ok {
		t.Fatal("invite accepted twice")
	}
	if err := s.Invites().Revoke(ctx, inv.ID, now); err == nil {
		t.Fatal("revoked an accepted invite")
	}
	if list, _ := s.Invites().ListPending(ctx, org.ID); len(list) != 0 {
		t.Fatalf("accepted invite still listed: %+v", list)
	}

	// Once the first invite is no longer pending the email can be invited again.
	if err := s.Invites().Create(ctx, &dup); err != nil {
		t.Fatalf("re-invite after accept: %v", err)
	}
	if err := s.Invites().Revoke(ctx, dup.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Invites().Rotate(ctx, dup.ID, "hash-3", newExp); err == nil {
		t.Fatal("rotated a revoked invite")
	}
	g, err := s.Invites().GetByID(ctx, dup.ID)
	if err != nil || g.RevokedAt == nil || g.Pending() {
		t.Fatalf("GetByID after revoke = %+v, %v", g, err)
	}
}
