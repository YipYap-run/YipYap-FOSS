package contactmethod_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func TestSealExistingSealsPlaintextRows(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: uuid.NewString(), Name: "Seal", Slug: "seal-" + uuid.NewString()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: uuid.NewString(), OrgID: org.ID, Email: "seal@example.com", PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	add := func(typ domain.ContactMethodType, cfg string) *domain.ContactMethod {
		m := &domain.ContactMethod{ID: uuid.NewString(), OrgID: org.ID, UserID: u.ID, Type: typ, Config: cfg, CreatedAt: now, UpdatedAt: now}
		if err := s.ContactMethods().Create(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	push := add(domain.ContactPushover, `{"user_key":"u","api_token":"plain-token"}`)
	hook := add(domain.ContactWebhook, `{"url":"https://example.com/h","secret":"plain-secret-123"}`)
	mail := add(domain.ContactEmail, `{"address":"seal@example.com"}`)

	enc := func(p []byte) ([]byte, error) { return append([]byte("x"), p...), nil }
	n, err := contactmethod.SealExisting(ctx, s, enc)
	if err != nil || n != 2 {
		t.Fatalf("sealed = %d, %v, want 2", n, err)
	}
	for _, m := range []*domain.ContactMethod{push, hook} {
		got, _ := s.ContactMethods().Get(ctx, u.ID, m.ID)
		if strings.Contains(got.Config, "plain-") || !strings.Contains(got.Config, "sealed:v1:") {
			t.Fatalf("%s config not sealed: %s", m.Type, got.Config)
		}
	}
	if got, _ := s.ContactMethods().Get(ctx, u.ID, mail.ID); got.Config != mail.Config {
		t.Fatalf("email config changed: %s", got.Config)
	}
	if n, err := contactmethod.SealExisting(ctx, s, enc); err != nil || n != 0 {
		t.Fatalf("second run sealed %d, %v, want 0", n, err)
	}
}
