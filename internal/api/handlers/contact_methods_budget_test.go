package handlers_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestContactMethodSendBudgetReturns429(t *testing.T) {
	f := newCMFixture(t)
	code, m, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "email", "label": "Budgets", "config": map[string]any{"address": "budget@example.com"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, m)
	}
	id := m["id"].(string)

	member, err := f.s.Users().GetByEmail(context.Background(), "cm-member@test.com")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		ok, err := f.s.ContactMethods().ReserveSendBudget(context.Background(), member.ID, member.OrgID, time.Now().UTC(), false, 20, 50)
		if err != nil || !ok {
			t.Fatalf("reserve %d = %v, %v", i, ok, err)
		}
	}

	resp := doJSONReq(t, http.MethodPost, f.srv.URL+"/api/v1/users/me/contact-methods/"+id+"/send-code", f.member.token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("send-code after budget = %d", resp.StatusCode)
	}
	if _, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil {
		t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
}
