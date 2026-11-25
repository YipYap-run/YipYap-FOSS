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
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// checkSessionCookie asserts resp carries an HttpOnly, SameSite=Strict
// yipyap_session cookie equal to token, Secure when wantSecure.
func checkSessionCookie(t *testing.T, step string, resp *http.Response, token string, wantSecure bool) {
	t.Helper()
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "yipyap_session" {
			got = c
		}
	}
	if got == nil {
		t.Fatalf("%s: no yipyap_session Set-Cookie", step)
	}
	if got.Value != token || token == "" {
		t.Errorf("%s: cookie value does not match response token", step)
	}
	if !got.HttpOnly {
		t.Errorf("%s: cookie not HttpOnly", step)
	}
	if got.SameSite != http.SameSiteStrictMode {
		t.Errorf("%s: SameSite = %v, want Strict", step, got.SameSite)
	}
	if got.Secure != wantSecure {
		t.Errorf("%s: Secure = %v, want %v", step, got.Secure, wantSecure)
	}
	if got.MaxAge <= 0 {
		t.Errorf("%s: MaxAge = %d, want > 0", step, got.MaxAge)
	}
}

func TestPasswordLoginPathsSetSessionCookie(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseURL string
		secure  bool
	}{
		{"https", "https://app.example.test", true},
		{"http", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := sqlite.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			jwt := auth.NewJWTIssuer([]byte("cookie-path-secret"), time.Hour)
			ts := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", tc.baseURL,
				api.ServerOptions{RegistrationEnabled: true}))
			t.Cleanup(ts.Close)

			type tokenBody struct {
				Token string `json:"token"`
			}
			post := func(path string, body interface{}, sessionCookie string) (*http.Response, tokenBody) {
				t.Helper()
				b, _ := json.Marshal(body)
				req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				if sessionCookie != "" {
					// Send the session the way a browser does after a reload:
					// only the cookie, no Authorization header.
					req.AddCookie(&http.Cookie{Name: "yipyap_session", Value: sessionCookie})
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				var tb tokenBody
				_ = json.NewDecoder(resp.Body).Decode(&tb)
				return resp, tb
			}

			// No mailer configured, so register issues a session directly.
			resp, reg := post("/api/v1/auth/register", map[string]string{
				"org_name": "Cookie Org", "email": "cookie@example.com",
				"password": "supersecret123", "confirm_password": "supersecret123",
			}, "")
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("register status = %d", resp.StatusCode)
			}
			checkSessionCookie(t, "register", resp, reg.Token, tc.secure)

			resp, login := post("/api/v1/auth/login", map[string]string{
				"email": "cookie@example.com", "password": "supersecret123",
			}, "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("login status = %d", resp.StatusCode)
			}
			checkSessionCookie(t, "login", resp, login.Token, tc.secure)

			resp, ref := post("/api/v1/auth/refresh", map[string]string{}, login.Token)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("refresh status = %d", resp.StatusCode)
			}
			checkSessionCookie(t, "refresh", resp, ref.Token, tc.secure)
		})
	}
}
