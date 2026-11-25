package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// The API Keys tab (web/src/pages/settings/api-keys.jsx) depends on these
// response shapes and on the exact expires_at format the browser sends.

func TestAPIKeyUIContract_CreateAcceptsBrowserISOTime(t *testing.T) {
	f := newSmokeFixture(t)
	// Date.prototype.toISOString always includes milliseconds.
	at := time.Now().UTC().Add(30 * 24 * time.Hour)
	sent := at.Format("2006-01-02T15:04:05.000Z")
	code, body := f.call(f.sessionToken, http.MethodPost, "/api/v1/org/api-keys", map[string]any{
		"name": "ui", "scopes": []string{"monitors:write", "alerts:read"}, "expires_at": sent,
	})
	if code != http.StatusCreated {
		t.Fatalf("got %d: %s", code, body)
	}
	var c struct {
		Key       string     `json:"key"`
		Name      string     `json:"name"`
		Prefix    string     `json:"prefix"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	if c.Key == "" || c.Name != "ui" || c.Prefix == "" || len(c.Scopes) != 2 {
		t.Errorf("create response missing fields the UI shows: %s", body)
	}
	if c.ExpiresAt == nil || !c.ExpiresAt.Equal(at.Truncate(time.Second)) {
		t.Errorf("expires_at = %v, want %v", c.ExpiresAt, at.Truncate(time.Second))
	}

	// The custom date picker's latest choice is the end of the day 729 days
	// out, which must still be inside the server cap.
	late := time.Now().AddDate(0, 0, 729)
	late = time.Date(late.Year(), late.Month(), late.Day(), 23, 59, 59, 0, time.Local).UTC()
	code, body = f.call(f.sessionToken, http.MethodPost, "/api/v1/org/api-keys", map[string]any{
		"name": "late", "scopes": []string{"read-only"}, "expires_at": late.Format("2006-01-02T15:04:05.000Z"),
	})
	if code != http.StatusCreated {
		t.Errorf("latest picker date rejected: %d %s", code, body)
	}
}

func TestAPIKeyUIContract_VocabularyAndList(t *testing.T) {
	f := newSmokeFixture(t)
	code, body := f.call(f.sessionToken, http.MethodGet, "/api/v1/org/api-keys/scopes", nil)
	if code != http.StatusOK {
		t.Fatalf("scopes: %d %s", code, body)
	}
	var v struct {
		Presets []struct {
			Key, Label, Description string
		} `json:"presets"`
		Resources []struct {
			Key, Label, Description string
			Actions                 []string
		} `json:"resources"`
		DefaultExpiryDays int `json:"default_expiry_days"`
		MaxExpiryDays     int `json:"max_expiry_days"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if v.MaxExpiryDays != 730 || v.DefaultExpiryDays != 90 {
		t.Errorf("expiry bounds = %d/%d", v.DefaultExpiryDays, v.MaxExpiryDays)
	}
	for _, p := range v.Presets {
		if p.Label == "" || p.Description == "" {
			t.Errorf("preset %q lacks label or description", p.Key)
		}
	}
	for _, r := range v.Resources {
		if r.Label == "" || r.Description == "" || len(r.Actions) == 0 || r.Actions[0] != "read" {
			t.Errorf("resource %+v is not renderable", r)
		}
	}

	if code, body := f.call(f.sessionToken, http.MethodPost, "/api/v1/org/api-keys", map[string]any{
		"name": "listed", "scopes": []string{"read-only"},
	}); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	// The tab pages through the list with limit and offset.
	code, body = f.call(f.sessionToken, http.MethodGet, "/api/v1/org/api-keys?limit=100&offset=0", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	var found map[string]any
	for _, k := range list {
		if k["name"] == "listed" {
			found = k
		}
	}
	if found == nil {
		t.Fatalf("created key missing from list: %s", body)
	}
	for _, field := range []string{"id", "prefix", "scopes", "expires_at", "created_at", "creator_email", "creator_removed", "expired", "unrestricted"} {
		if _, ok := found[field]; !ok {
			t.Errorf("list entry lacks %q: %v", field, found)
		}
	}
	if found["creator_email"] != f.ownerEmail {
		t.Errorf("creator_email = %v, want %s", found["creator_email"], f.ownerEmail)
	}
}

// The list row flags expired keys and shows when a key was last used; the
// revoke dialog removes a key by id.
func TestAPIKeyUIContract_ExpiredLastUsedAndRevoke(t *testing.T) {
	f := newSmokeFixture(t)
	owner, err := f.store.Users().GetByEmail(context.Background(), f.ownerEmail)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	f.insertKey("old-key", owner.ID, []string{"read-only"}, &past)
	used := f.insertKey("used-key", owner.ID, []string{"read-only"}, nil)

	if code, body := f.call(used, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusOK {
		t.Fatalf("using key: %d %s", code, body)
	}

	listed := func() map[string]map[string]any {
		t.Helper()
		code, body := f.call(f.sessionToken, http.MethodGet, "/api/v1/org/api-keys?limit=100&offset=0", nil)
		if code != http.StatusOK {
			t.Fatalf("list: %d %s", code, body)
		}
		var list []map[string]any
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]any{}
		for _, k := range list {
			out[k["id"].(string)] = k
		}
		return out
	}

	keys := listed()
	if keys["old-key"]["expired"] != true {
		t.Errorf("expired key not flagged: %v", keys["old-key"])
	}
	if keys["used-key"]["expired"] != false {
		t.Errorf("live key flagged expired: %v", keys["used-key"])
	}
	// last_used_at is written in the background after auth; wait for it.
	deadline := time.Now().Add(3 * time.Second)
	for keys["used-key"]["last_used_at"] == nil && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		keys = listed()
	}
	if s, _ := keys["used-key"]["last_used_at"].(string); s == "" {
		t.Errorf("last_used_at not reported after use: %v", keys["used-key"])
	}

	if code, body := f.call(f.sessionToken, http.MethodDelete, "/api/v1/org/api-keys/used-key", nil); code/100 != 2 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if _, ok := listed()["used-key"]; ok {
		t.Error("revoked key still listed")
	}
	if code, _ := f.call(used, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusUnauthorized {
		t.Errorf("revoked key: got %d, want 401", code)
	}
}

// The member removal dialog checks GET /users/{id}/api-keys first and, when
// the member created keys, always sends revoke_api_keys=true or =false.
func TestAPIKeyUIContract_MemberRemovalPrompt(t *testing.T) {
	f := newSmokeFixture(t)
	ctx := context.Background()
	owner, _ := f.store.Users().GetByEmail(ctx, f.ownerEmail)
	now := time.Now().UTC()
	for _, id := range []string{"nokeys", "haskeys"} {
		if err := f.store.Users().Create(ctx, &domain.User{
			ID: id, OrgID: owner.OrgID, Email: id + "@example.com", Name: id,
			PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	key := f.insertKey("hk-1", "haskeys", []string{"read-only"}, nil)

	// No keys: count 0 and an empty array (not null), so no prompt.
	code, body := f.call(f.sessionToken, http.MethodGet, "/api/v1/users/nokeys/api-keys", nil)
	if code != http.StatusOK {
		t.Fatalf("preflight: %d %s", code, body)
	}
	var pre struct {
		Count int               `json:"count"`
		Keys  []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal([]byte(body), &pre); err != nil {
		t.Fatal(err)
	}
	if pre.Count != 0 || pre.Keys == nil {
		t.Errorf("no-keys preflight = %s, want count 0 and keys []", body)
	}

	// Keys listed for the prompt carry the fields the dialog renders.
	code, body = f.call(f.sessionToken, http.MethodGet, "/api/v1/users/haskeys/api-keys", nil)
	if code != http.StatusOK {
		t.Fatalf("preflight: %d %s", code, body)
	}
	pre.Keys = nil
	if err := json.Unmarshal([]byte(body), &pre); err != nil {
		t.Fatal(err)
	}
	if pre.Count != 1 || len(pre.Keys) != 1 {
		t.Fatalf("haskeys preflight = %s", body)
	}
	var k map[string]any
	_ = json.Unmarshal(pre.Keys[0], &k)
	for _, field := range []string{"id", "name", "prefix", "expires_at"} {
		if _, ok := k[field]; !ok {
			t.Errorf("preflight key lacks %q: %v", field, k)
		}
	}

	// "Keep them working" sends revoke_api_keys=false: removal succeeds and
	// the key keeps working.
	if code, body := f.call(f.sessionToken, http.MethodDelete, "/api/v1/users/haskeys?revoke_api_keys=false", nil); code/100 != 2 {
		t.Fatalf("remove keeping keys: %d %s", code, body)
	}
	if code, _ := f.call(key, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusOK {
		t.Errorf("kept key: got %d, want 200", code)
	}
}
