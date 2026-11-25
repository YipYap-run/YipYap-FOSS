package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func TestRegisterVerifySeedsPagingTarget(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)
	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{
		RegistrationEnabled: true,
		Mailer:              mailer.New(mailer.Config{Host: "127.0.0.1", Port: 1, From: "noreply@test.local"}),
	}))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{
		"org_name": "Verify Org", "email": "verify@test.com", "password": "supersecret123",
	})
	resp, err := http.Post(srv.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d", resp.StatusCode)
	}

	user, err := s.Users().GetByEmail(context.Background(), "verify@test.com")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := s.NotificationRules().ListPagingTargets(context.Background(), user.ID)
	if err != nil || len(targets) != 0 {
		t.Fatalf("unverified targets = %+v, %v", targets, err)
	}

	token, err := jwt.IssueEmailVerification(user.ID, user.OrgID, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/auth/verify-email", "", map[string]string{"token": token})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("verify %d: %d", i, resp.StatusCode)
		}
	}

	targets, err = s.NotificationRules().ListPagingTargets(context.Background(), user.ID)
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets after verify = %+v, %v", targets, err)
	}
	if targets[0].Method.Type != "email" || !targets[0].Method.Verified() {
		t.Fatalf("target = %+v", targets[0].Method)
	}
}
