package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// The Settings > Sessions page reads these fields and codes. This test pins
// the contract so a backend change cannot break the page silently.

const contractUA = "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0"

func (e *sessEnv) raw(token, method, path, ua string, payload interface{}) *http.Response {
	e.t.Helper()
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestSessions_ListContractForSessionsPage(t *testing.T) {
	e := newSessEnv(t)
	resp := e.raw("", http.MethodPost, "/api/v1/auth/login", contractUA,
		map[string]string{"email": sessOwnerEmail, "password": sessOwnerPassword})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil || login.Token == "" {
		t.Fatalf("login token: %v", err)
	}

	resp = e.raw(login.Token, http.MethodGet, "/api/v1/auth/sessions", contractUA, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var list struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) == 0 {
		t.Fatal("no sessions listed")
	}

	methods := map[string]bool{"password": true, "mfa": true, "passkey": true, "sso": true, "impersonation": true}
	var current map[string]interface{}
	for _, s := range list.Sessions {
		for _, k := range []string{"id", "auth_method", "created_at", "last_seen_at", "expires_at", "user_agent", "ip", "current"} {
			if _, ok := s[k]; !ok {
				t.Fatalf("session is missing %q: %v", k, s)
			}
		}
		if id, _ := s["id"].(string); id == "" {
			t.Fatalf("empty id: %v", s)
		}
		if m, _ := s["auth_method"].(string); !methods[m] {
			t.Fatalf("auth_method %q is not one the page labels", m)
		}
		for _, k := range []string{"created_at", "last_seen_at", "expires_at"} {
			v, _ := s[k].(string)
			ts, err := time.Parse(time.RFC3339, v)
			if err != nil || ts.IsZero() {
				t.Fatalf("%s = %q, want an RFC 3339 time", k, v)
			}
		}
		if _, ok := s["current"].(bool); !ok {
			t.Fatalf("current is not a bool: %v", s["current"])
		}
		if s["current"] == true {
			if current != nil {
				t.Fatal("two sessions marked current")
			}
			current = s
		}
	}
	if current == nil {
		t.Fatal("no session marked current")
	}
	if current["user_agent"] != contractUA {
		t.Fatalf("user_agent = %v, want the login request's", current["user_agent"])
	}
	if ip, _ := current["ip"].(string); ip == "" {
		t.Fatal("ip is empty")
	}
	exp, _ := time.Parse(time.RFC3339, current["expires_at"].(string))
	if !exp.After(time.Now()) {
		t.Fatalf("current session expires_at %v is not in the future", exp)
	}

	// Signing out the current session clears the cookie.
	resp = e.raw(login.Token, http.MethodDelete, "/api/v1/auth/sessions/"+current["id"].(string), contractUA, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke current: %d", resp.StatusCode)
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "yipyap_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("revoking the current session did not clear the session cookie")
	}

	// The page sends the user to /login?reason=signed_out on a 401 with
	// this code, from both /auth/sessions and any other route.
	for _, path := range []string{"/api/v1/auth/sessions", "/api/v1/org"} {
		resp = e.raw(login.Token, http.MethodGet, path, contractUA, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s after revoke = %d, want 401", path, resp.StatusCode)
		}
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if out["code"] != "session_revoked" {
			t.Fatalf("GET %s code = %q, want session_revoked", path, out["code"])
		}
	}
}

// "Sign out of all other sessions" posts an empty body, expects 204 with no
// body, and then re-reads the list, which must hold only the current session.
func TestSessions_RevokeOthersContractForSessionsPage(t *testing.T) {
	e := newSessEnv(t)
	current := e.login(sessOwnerEmail, sessOwnerPassword)
	other := e.login(sessOwnerEmail, sessOwnerPassword)

	resp := e.raw(current, http.MethodPost, "/api/v1/auth/sessions/revoke-others", contractUA, map[string]string{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke-others: %d", resp.StatusCode)
	}
	if b, _ := io.ReadAll(resp.Body); len(b) != 0 {
		t.Fatalf("revoke-others body = %q, want empty", b)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "yipyap_session" && c.MaxAge < 0 {
			t.Fatal("revoke-others cleared the current session cookie")
		}
	}

	resp = e.raw(current, http.MethodGet, "/api/v1/auth/sessions", contractUA, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var list struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || !list.Sessions[0].Current || list.Sessions[0].ID != e.sid(current) {
		t.Fatalf("after revoke-others list = %+v, want only the current session", list.Sessions)
	}
	if e.works(other) {
		t.Fatal("other session still accepted")
	}

	// With no other sessions left the call is still a 204, so a double
	// click does not show an error.
	resp = e.raw(current, http.MethodPost, "/api/v1/auth/sessions/revoke-others", contractUA, map[string]string{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second revoke-others: %d", resp.StatusCode)
	}

	// An unknown id is a 404 the page reads as "already ended".
	resp = e.raw(current, http.MethodDelete, "/api/v1/auth/sessions/does-not-exist", contractUA, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id: %d, want 404", resp.StatusCode)
	}
}
