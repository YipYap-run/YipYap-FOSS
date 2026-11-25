package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// The browser cannot read the HTTP status of a refused WebSocket upgrade.
// When /ws refuses a session, web/src/api/ws.js asks GET /api/v1/org the
// same question and maps the answer to the banner it shows (stopReasonFor):
// 401 means the session ended, a 403 whose error mentions "disabled",
// "password" or "mfa" names the fix. These tests pin that contract: for
// each refused state, /ws and /org agree on the status, and the /org error
// carries the word the SPA looks for.

type wsContractFixture struct {
	*wsFixture
	full *httptest.Server
}

func newWSContractFixture(t *testing.T) *wsContractFixture {
	t.Helper()
	f := newWSFixture(t)
	full := httptest.NewServer(NewServer(f.store, f.jwt, bus.NewChannel(), f.hub, nil, "", ""))
	t.Cleanup(full.Close)
	return &wsContractFixture{wsFixture: f, full: full}
}

func (f *wsContractFixture) getOrg(t *testing.T, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.full.URL+"/api/v1/org", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.Header.Set("Cookie", "yipyap_session="+cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body.Error
}

// assertWSAndOrgAgree checks that /ws refuses with want, and that /org
// returns the same status with an error mentioning keyword ("" for 401).
func (f *wsContractFixture) assertWSAndOrgAgree(t *testing.T, cookie string, want int, keyword string) {
	t.Helper()
	conn, resp, err := f.dial(t, cookie, "")
	expectRejected(t, conn, resp, err, want)
	status, msg := f.getOrg(t, cookie)
	if status != want {
		t.Fatalf("/api/v1/org returned %d, /ws returned %d; the SPA session check would misread this", status, want)
	}
	if keyword != "" && !strings.Contains(strings.ToLower(msg), keyword) {
		t.Fatalf("/api/v1/org error %q does not mention %q, which the SPA maps to its banner", msg, keyword)
	}
}

func TestWSContractValidSessionReadsOrg(t *testing.T) {
	f := newWSContractFixture(t)
	u := f.addUser(t, "fine", nil)
	if status, _ := f.getOrg(t, f.token(t, u)); status != http.StatusOK {
		t.Fatalf("valid session: /api/v1/org returned %d, want 200", status)
	}
}

func TestWSContractExpiredSession(t *testing.T) {
	f := newWSContractFixture(t)
	f.assertWSAndOrgAgree(t, "not-a-jwt", http.StatusUnauthorized, "")
}

func TestWSContractDisabledUser(t *testing.T) {
	f := newWSContractFixture(t)
	u := f.addUser(t, "off", nil)
	if err := f.store.Users().Disable(context.Background(), u.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f.assertWSAndOrgAgree(t, f.token(t, u), http.StatusForbidden, "disabled")
}

func TestWSContractForcedPasswordChange(t *testing.T) {
	f := newWSContractFixture(t)
	u := f.addUser(t, "pw", func(u *domain.User) { u.ForcePasswordChange = true })
	f.assertWSAndOrgAgree(t, f.token(t, u), http.StatusForbidden, "password")
}
