package handlers_test

import (
	"net/http"
	"strings"
	"testing"
)

// The policy form saves a description; it must come back from create, get,
// list and update, and a PATCH without it must leave it alone (ux-9).
func TestEscalationPolicyDescriptionRoundTrip(t *testing.T) {
	srv, _, _ := setupCloudEventTestServer(t)
	c := cmClient{t, srv.URL, ceRegisterAndGetToken(t, srv, "Desc Org", "desc@test.com", "supersecret123")}

	code, created, _ := c.do(http.MethodPost, "/escalation-policies", map[string]any{
		"name": "  Payments  ", "description": "  Pages the payments on-call first.  ",
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	if created["name"] != "Payments" || created["description"] != "Pages the payments on-call first." {
		t.Fatalf("create response not trimmed or missing description: %v", created)
	}
	id, _ := created["id"].(string)

	_, got, _ := c.do(http.MethodGet, "/escalation-policies/"+id, nil)
	if got["description"] != "Pages the payments on-call first." {
		t.Fatalf("get description = %v", got["description"])
	}

	_, _, list := c.do(http.MethodGet, "/escalation-policies", nil)
	if len(list) != 1 || list[0].(map[string]any)["description"] != "Pages the payments on-call first." {
		t.Fatalf("list = %v", list)
	}

	// Rename only: description stays.
	code, upd, _ := c.do(http.MethodPatch, "/escalation-policies/"+id, map[string]any{"name": "Payments primary"})
	if code != http.StatusOK || upd["description"] != "Pages the payments on-call first." {
		t.Fatalf("rename: %d %v", code, upd)
	}

	// Clear it explicitly.
	code, upd, _ = c.do(http.MethodPatch, "/escalation-policies/"+id, map[string]any{"description": ""})
	if code != http.StatusOK || upd["description"] != "" {
		t.Fatalf("clear: %d %v", code, upd)
	}
	_, got, _ = c.do(http.MethodGet, "/escalation-policies/"+id, nil)
	if got["description"] != "" || got["name"] != "Payments primary" {
		t.Fatalf("after clear: %v", got)
	}
}

func TestEscalationPolicyFieldValidation(t *testing.T) {
	srv, _, _ := setupCloudEventTestServer(t)
	c := cmClient{t, srv.URL, ceRegisterAndGetToken(t, srv, "Val Org", "polval@test.com", "supersecret123")}

	code, body, _ := c.do(http.MethodPost, "/escalation-policies", map[string]any{"name": "   "})
	if code != http.StatusBadRequest || body["field"] != "name" {
		t.Fatalf("blank name: %d %v", code, body)
	}
	code, body, _ = c.do(http.MethodPost, "/escalation-policies", map[string]any{
		"name": "ok", "description": strings.Repeat("x", 2001),
	})
	if code != http.StatusBadRequest || body["field"] != "description" {
		t.Fatalf("long description: %d %v", code, body)
	}

	code, created, _ := c.do(http.MethodPost, "/escalation-policies", map[string]any{"name": "ok"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	if _, has := created["description"]; !has {
		t.Fatalf("description must always be present for the form: %v", created)
	}
	id, _ := created["id"].(string)
	code, body, _ = c.do(http.MethodPatch, "/escalation-policies/"+id, map[string]any{"name": ""})
	if code != http.StatusBadRequest || body["field"] != "name" {
		t.Fatalf("patch blank name: %d %v", code, body)
	}
}
