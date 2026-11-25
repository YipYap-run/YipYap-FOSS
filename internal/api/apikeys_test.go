package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func (f *smokeFixture) call(token, method, path string, body any) (int, string) {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// insertKey stores a key directly so tests can set its creator and expiry.
func (f *smokeFixture) insertKey(id, createdBy string, scopes []string, expiresAt *time.Time) string {
	f.t.Helper()
	plaintext, hash, prefix, err := f.hasher.GenerateAPIKey()
	if err != nil {
		f.t.Fatal(err)
	}
	owner, err := f.store.Users().GetByEmail(context.Background(), f.ownerEmail)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.APIKeys().Create(context.Background(), &domain.APIKey{
		ID: id, OrgID: owner.OrgID, Name: id, KeyHash: hash, Prefix: prefix,
		Scopes: scopes, CreatedBy: createdBy, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC(),
	}); err != nil {
		f.t.Fatal(err)
	}
	return plaintext
}

func TestAPIKeyCreate_ExpiryAndScopeValidation(t *testing.T) {
	f := newSmokeFixture(t)
	now := time.Now().UTC()

	bad := []struct {
		name string
		body map[string]any
	}{
		{"unknown scope", map[string]any{"name": "k", "scopes": []string{"monitors:admin"}}},
		{"free text scope", map[string]any{"name": "k", "scopes": []string{"monitors"}}},
		{"no scopes", map[string]any{"name": "k"}},
		{"past expiry", map[string]any{"name": "k", "scopes": []string{"read-only"}, "expires_at": now.Add(-time.Hour).Format(time.RFC3339)}},
		{"expiry too far out", map[string]any{"name": "k", "scopes": []string{"read-only"}, "expires_at": now.AddDate(3, 0, 0).Format(time.RFC3339)}},
		{"expiry not a time", map[string]any{"name": "k", "scopes": []string{"read-only"}, "expires_at": "next week"}},
		{"name too long", map[string]any{"name": strings.Repeat("k", 101), "scopes": []string{"read-only"}}},
	}
	for _, c := range bad {
		code, body := f.call(f.sessionToken, http.MethodPost, "/api/v1/org/api-keys", c.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: got %d (%s), want 400", c.name, code, body)
		}
	}

	type created struct {
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
		CreatedBy string     `json:"created_by"`
	}
	create := func(body map[string]any) created {
		t.Helper()
		code, resp := f.call(f.sessionToken, http.MethodPost, "/api/v1/org/api-keys", body)
		if code != http.StatusCreated {
			t.Fatalf("create %v: got %d: %s", body, code, resp)
		}
		var c created
		if err := json.Unmarshal([]byte(resp), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}

	def := create(map[string]any{"name": "default", "scopes": []string{"monitors:read", "monitors:write"}})
	if def.ExpiresAt == nil {
		t.Fatal("omitted expires_at should default to 90 days, got no expiry")
	}
	if d := def.ExpiresAt.Sub(now.AddDate(0, 0, 90)); d < -time.Minute || d > time.Minute {
		t.Errorf("default expiry %v is not 90 days out", def.ExpiresAt)
	}
	if strings.Join(def.Scopes, ",") != "monitors:write" {
		t.Errorf("scopes not normalized: %v", def.Scopes)
	}
	if def.CreatedBy == "" {
		t.Error("created_by missing from create response")
	}

	if never := create(map[string]any{"name": "never", "scopes": []string{"read-only"}, "expires_at": "never"}); never.ExpiresAt != nil {
		t.Errorf(`"never" should store no expiry, got %v`, never.ExpiresAt)
	}
	if null := create(map[string]any{"name": "null", "scopes": []string{"read-only"}, "expires_at": nil}); null.ExpiresAt != nil {
		t.Errorf("null should store no expiry, got %v", null.ExpiresAt)
	}
	at := now.Add(48 * time.Hour).Truncate(time.Second)
	if exp := create(map[string]any{"name": "dated", "scopes": []string{"read-only"}, "expires_at": at.Format(time.RFC3339)}); exp.ExpiresAt == nil || !exp.ExpiresAt.Equal(at) {
		t.Errorf("explicit expiry: got %v, want %v", exp.ExpiresAt, at)
	}
}

func TestAPIKeyScopes_VocabularyEndpoint(t *testing.T) {
	f := newSmokeFixture(t)
	code, body := f.call(f.sessionToken, http.MethodGet, "/api/v1/org/api-keys/scopes", nil)
	if code != http.StatusOK {
		t.Fatalf("got %d: %s", code, body)
	}
	var v struct {
		Presets []struct {
			Key string `json:"key"`
		} `json:"presets"`
		Resources []struct {
			Key     string   `json:"key"`
			Actions []string `json:"actions"`
		} `json:"resources"`
		DefaultExpiryDays int `json:"default_expiry_days"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Presets) != 2 || v.Presets[0].Key != "read-only" || v.Presets[1].Key != "full-access" {
		t.Errorf("presets = %+v", v.Presets)
	}
	if len(v.Resources) == 0 || v.Resources[0].Key != "monitors" || len(v.Resources[0].Actions) != 2 {
		t.Errorf("resources = %+v", v.Resources)
	}
	if v.DefaultExpiryDays != 90 {
		t.Errorf("default_expiry_days = %d", v.DefaultExpiryDays)
	}
	// An API key cannot read the vocabulary or manage keys.
	code, _ = f.call(f.apiKeyAll, http.MethodGet, "/api/v1/org/api-keys/scopes", nil)
	if code != http.StatusForbidden {
		t.Errorf("api key reading scopes: got %d, want 403", code)
	}
}

func TestAPIKeyAuth_ExpiredKeyRejected(t *testing.T) {
	f := newSmokeFixture(t)
	owner, _ := f.store.Users().GetByEmail(context.Background(), f.ownerEmail)
	past := time.Now().UTC().Add(-time.Minute)
	future := time.Now().UTC().Add(time.Hour)
	expired := f.insertKey("expired", owner.ID, []string{"full-access"}, &past)
	live := f.insertKey("live", owner.ID, []string{"full-access"}, &future)

	if code, body := f.call(expired, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusUnauthorized || !strings.Contains(body, "expired") {
		t.Errorf("expired key: got %d %s, want 401 api key expired", code, body)
	}
	if code, body := f.call(live, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusOK {
		t.Errorf("live key: got %d %s, want 200", code, body)
	}
}

func TestAPIKeyScope_ReadOnlyKeyCannotWrite(t *testing.T) {
	f := newSmokeFixture(t)
	ro := f.mintAPIKey("ro", []string{"read-only"})
	if code, body := f.call(ro, http.MethodGet, "/api/v1/users", nil); code != http.StatusOK {
		t.Errorf("read-only GET /users: got %d %s", code, body)
	}
	if code, body := f.call(ro, http.MethodPost, "/api/v1/monitors", map[string]any{"name": "x"}); code != http.StatusForbidden {
		t.Errorf("read-only POST /monitors: got %d %s, want 403", code, body)
	}
	// /users and /org had no RequireScope; a key scoped to monitors must
	// not read them.
	mon := f.mintAPIKey("mon", []string{"monitors:write"})
	for _, p := range []string{"/api/v1/users", "/api/v1/org"} {
		if code, body := f.call(mon, http.MethodGet, p, nil); code != http.StatusForbidden {
			t.Errorf("monitors key GET %s: got %d %s, want 403", p, code, body)
		}
	}
	// Account routes are never open to API keys, even legacy unscoped ones.
	if code, body := f.call(f.apiKeyNoScopes, http.MethodPut, "/api/v1/auth/password", map[string]any{"current_password": "x", "new_password": "y"}); code != http.StatusForbidden {
		t.Errorf("legacy key PUT /auth/password: got %d %s, want 403", code, body)
	}
	if code, body := f.call(f.apiKeyNoScopes, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusOK {
		t.Errorf("legacy key GET /monitors: got %d %s, want 200", code, body)
	}
}

func TestUserDelete_APIKeysOrgOwnedWithOptionalRevoke(t *testing.T) {
	f := newSmokeFixture(t)
	ctx := context.Background()
	owner, _ := f.store.Users().GetByEmail(ctx, f.ownerEmail)
	now := time.Now().UTC()
	mkAdmin := func(id string) {
		if err := f.store.Users().Create(ctx, &domain.User{
			ID: id, OrgID: owner.OrgID, Email: id + "@example.com", Name: "Admin " + id,
			PasswordHash: "x", Role: domain.RoleAdmin, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mkAdmin("keep")
	mkAdmin("revoke")
	keepKey := f.insertKey("keep-1", "keep", []string{"monitors:read"}, nil)
	f.insertKey("keep-2", "keep", []string{"monitors:read"}, nil)
	revokeKey := f.insertKey("revoke-1", "revoke", []string{"monitors:read"}, nil)
	f.insertKey("revoke-2", "revoke", []string{"monitors:read"}, nil)

	// The preflight list tells the UI how many keys the person created.
	code, body := f.call(f.sessionToken, http.MethodGet, "/api/v1/users/keep/api-keys", nil)
	if code != http.StatusOK || !strings.Contains(body, `"count":2`) || !strings.Contains(body, `"creator_email":"keep@example.com"`) {
		t.Fatalf("preflight: got %d %s", code, body)
	}
	if code, _ := f.call(f.apiKeyAll, http.MethodGet, "/api/v1/users/keep/api-keys", nil); code != http.StatusForbidden {
		t.Errorf("api key reading preflight: got %d, want 403", code)
	}

	// Without the flag the keys stay and keep working (org-owned).
	if code, body := f.call(f.sessionToken, http.MethodDelete, "/api/v1/users/keep", nil); code != http.StatusNoContent {
		t.Fatalf("delete without revoke: got %d %s", code, body)
	}
	if code, body := f.call(keepKey, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusOK {
		t.Errorf("orphaned key: got %d %s, want 200", code, body)
	}
	code, body = f.call(f.sessionToken, http.MethodGet, "/api/v1/org/api-keys", nil)
	if code != http.StatusOK || !strings.Contains(body, `"creator_removed":true`) {
		t.Errorf("list should flag the removed creator: %d %s", code, body)
	}

	// Invalid flag is rejected before anything changes.
	if code, _ := f.call(f.sessionToken, http.MethodDelete, "/api/v1/users/revoke?revoke_api_keys=maybe", nil); code != http.StatusBadRequest {
		t.Errorf("bad flag: got %d, want 400", code)
	}

	// With the flag the keys go in the same operation.
	code, body = f.call(f.sessionToken, http.MethodDelete, "/api/v1/users/revoke?revoke_api_keys=true", nil)
	if code != http.StatusOK || !strings.Contains(body, `"revoked_api_keys":2`) {
		t.Fatalf("delete with revoke: got %d %s", code, body)
	}
	if code, _ := f.call(revokeKey, http.MethodGet, "/api/v1/monitors", nil); code != http.StatusUnauthorized {
		t.Errorf("revoked key: got %d, want 401", code)
	}
	keys, err := f.store.APIKeys().ListByCreator(ctx, owner.OrgID, "keep")
	if err != nil || len(keys) != 2 {
		t.Errorf("kept keys = %d (%v), want 2", len(keys), err)
	}
}
