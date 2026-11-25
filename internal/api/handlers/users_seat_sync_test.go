package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type failingSeats struct{ added, removed int }

func (f *failingSeats) OnSeatAdded(context.Context, string) error {
	f.added++
	return errors.New("stripe unavailable")
}

func (f *failingSeats) OnSeatRemoved(context.Context, string) error {
	f.removed++
	return errors.New("stripe unavailable")
}

// A seat sync failure no longer disappears into the log: the member change
// still succeeds. Removal reports that billing was not updated; an invite
// accept still adds the member and logs the failure.
func TestMemberChangeSurfacesSeatSyncFailure(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "org-seatwarn", Name: "Seat Warn", Slug: "seat-warn", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	owner := &domain.User{ID: "owner-1", OrgID: org.ID, Email: "owner@seat.test", PasswordHash: "x", Role: domain.RoleOwner, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}

	seats := &failingSeats{}
	h := handlers.NewUserHandler(s)
	h.SetSeatUpdater(seats)
	claims := &auth.Claims{OrgID: org.ID, UserID: owner.ID, Role: string(domain.RoleOwner)}

	// A member now joins through an invite; accepting it adds the seat.
	h.SetInviteDelivery(nil, "https://app.test")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/invites",
		strings.NewReader(`{"email":"new@seat.test","role":"member"}`))
	req = req.WithContext(middleware.SetClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	h.CreateInvite(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var invited struct {
		AcceptURL string `json:"accept_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &invited); err != nil {
		t.Fatal(err)
	}
	token := invited.AcceptURL[strings.Index(invited.AcceptURL, "token=")+len("token="):]
	body, _ := json.Marshal(map[string]string{"token": token, "password": "longenough1"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/invites/accept", strings.NewReader(string(body)))
	rec = httptest.NewRecorder()
	h.AcceptInvite(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if seats.added != 1 {
		t.Fatalf("expected one seat sync on accept, got %d", seats.added)
	}
	newUser, err := s.Users().GetByEmail(ctx, "new@seat.test")
	if err != nil {
		t.Fatal(err)
	}
	created := struct{ ID string }{newUser.ID}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+created.ID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", created.ID)
	req = req.WithContext(context.WithValue(middleware.SetClaims(req.Context(), claims), chi.RouteCtxKey, rctx))
	rec = httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200 with a warning, got %d: %s", rec.Code, rec.Body.String())
	}
	var deleted map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted["billing_warning"] == "" || seats.removed != 1 {
		t.Fatalf("expected a billing_warning on delete, got %s", rec.Body.String())
	}
	gone, err := s.Users().GetByID(ctx, created.ID)
	if err != nil || gone.DeletedAt == nil {
		t.Fatalf("member should be anonymized even when the seat sync fails: %+v %v", gone, err)
	}
}
