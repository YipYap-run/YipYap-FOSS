package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type userListBody struct {
	Users  []domain.User `json:"users"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

func apiCall(t *testing.T, ts *httptest.Server, token, method, path string, body any) (int, map[string]any, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(resp.Body)
	m := map[string]any{}
	_ = json.Unmarshal(raw.Bytes(), &m)
	return resp.StatusCode, m, raw.Bytes()
}

func listUsers(t *testing.T, ts *httptest.Server, token, query string) userListBody {
	t.Helper()
	code, _, raw := apiCall(t, ts, token, http.MethodGet, "/api/v1/users"+query, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /users%s: %d %s", query, code, raw)
	}
	var out userListBody
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("GET /users%s: decode %v (%s)", query, err, raw)
	}
	return out
}

// TestUserListPagedWithTotal pins the member list contract (ux-3): the
// response is {users,total,limit,offset}, total counts every member,
// limit/offset page through them, q searches, role filters, and ids resolves
// a batch of IDs without leaking other orgs.
func TestUserListPagedWithTotal(t *testing.T) {
	ts, s := setupTestServer(t)
	ctx := context.Background()

	ownerToken := selfServisignupAndGetToken(t, ts, "ListOrg", "owner@list.test", "ownerpass1")
	owner, err := s.Users().GetByEmail(ctx, "owner@list.test")
	if err != nil {
		t.Fatal(err)
	}
	// 60 members plus the owner: more than the default page of 50.
	var lastID string
	for i := 0; i < 60; i++ {
		u := seedOrgUser(t, s, owner.OrgID, fmt.Sprintf("m%02d@list.test", i), "memberpass1", domain.RoleMember, "")
		lastID = u.ID
	}
	admin := seedOrgUser(t, s, owner.OrgID, "zed.admin@list.test", "adminpass1", domain.RoleAdmin, "")
	selfServisignupAndGetToken(t, ts, "OtherOrg", "owner@other.test", "otherpass1")
	outsider, err := s.Users().GetByEmail(ctx, "owner@other.test")
	if err != nil {
		t.Fatal(err)
	}

	first := listUsers(t, ts, ownerToken, "")
	if first.Total != 62 || len(first.Users) != 50 || first.Limit != 50 || first.Offset != 0 {
		t.Fatalf("default page: total=%d len=%d limit=%d offset=%d", first.Total, len(first.Users), first.Limit, first.Offset)
	}
	second := listUsers(t, ts, ownerToken, "?limit=50&offset=50")
	if second.Total != 62 || len(second.Users) != 12 {
		t.Fatalf("second page: total=%d len=%d, want 62 and 12", second.Total, len(second.Users))
	}

	found := listUsers(t, ts, ownerToken, "?q=ZED")
	if found.Total != 1 || len(found.Users) != 1 || found.Users[0].ID != admin.ID {
		t.Fatalf("q=ZED: %+v", found)
	}
	admins := listUsers(t, ts, ownerToken, "?role=admin&q=zed")
	if admins.Total != 1 || admins.Users[0].ID != admin.ID {
		t.Fatalf("role=admin: %+v", admins)
	}
	if code, _, _ := apiCall(t, ts, ownerToken, http.MethodGet, "/api/v1/users?role=superuser", nil); code != http.StatusBadRequest {
		t.Fatalf("bad role: want 400, got %d", code)
	}

	batch := listUsers(t, ts, ownerToken, "?ids="+url.QueryEscape(lastID+","+admin.ID+","+outsider.ID))
	if batch.Total != 2 || len(batch.Users) != 2 {
		t.Fatalf("ids lookup: total=%d len=%d, want the two org members only", batch.Total, len(batch.Users))
	}
	empty := listUsers(t, ts, ownerToken, "?q=nobody-matches")
	if empty.Users == nil || len(empty.Users) != 0 || empty.Total != 0 {
		t.Fatalf("no match: want empty array, got %+v", empty)
	}
}

// TestTransferOwnership covers the handler the Transfer Ownership modal
// calls (org-3): it needs user_id and the current password, step-up failures
// are 403 with a code (a 401 would sign the SPA out), and success swaps the
// roles.
func TestTransferOwnership(t *testing.T) {
	ts, s := setupTestServer(t)
	ctx := context.Background()

	ownerToken := selfServisignupAndGetToken(t, ts, "XferOrg", "owner@xfer.test", "ownerpass1")
	owner, err := s.Users().GetByEmail(ctx, "owner@xfer.test")
	if err != nil {
		t.Fatal(err)
	}
	admin := seedOrgUser(t, s, owner.OrgID, "admin@xfer.test", "adminpass1", domain.RoleAdmin, "")
	member := seedOrgUser(t, s, owner.OrgID, "member@xfer.test", "memberpass1", domain.RoleMember, "")

	const path = "/api/v1/org/transfer-ownership"

	// The old SPA payload: wrong field name.
	if code, _, _ := apiCall(t, ts, ownerToken, http.MethodPost, path, map[string]any{"target_user_id": admin.ID, "current_password": "ownerpass1"}); code != http.StatusBadRequest {
		t.Fatalf("target_user_id payload: want 400, got %d", code)
	}
	if code, _, _ := apiCall(t, ts, ownerToken, http.MethodPost, path, map[string]any{"user_id": admin.ID}); code != http.StatusBadRequest {
		t.Fatalf("missing password: want 400, got %d", code)
	}
	code, body, _ := apiCall(t, ts, ownerToken, http.MethodPost, path, map[string]any{"user_id": admin.ID, "current_password": "wrong-password"})
	if code != http.StatusForbidden || body["code"] != "invalid_password" {
		t.Fatalf("wrong password: want 403 invalid_password, got %d %v", code, body)
	}
	if code, _, _ := apiCall(t, ts, ownerToken, http.MethodPost, path, map[string]any{"user_id": member.ID, "current_password": "ownerpass1"}); code != http.StatusBadRequest {
		t.Fatalf("non-admin target: want 400, got %d", code)
	}

	// Nothing changed so far.
	if got, _ := s.Users().GetByID(ctx, owner.ID); got.Role != domain.RoleOwner {
		t.Fatalf("owner role changed after rejected requests: %s", got.Role)
	}

	code, body, _ = apiCall(t, ts, ownerToken, http.MethodPost, path, map[string]any{"user_id": admin.ID, "current_password": "ownerpass1"})
	if code != http.StatusOK {
		t.Fatalf("transfer: want 200, got %d %v", code, body)
	}
	if got, _ := s.Users().GetByID(ctx, admin.ID); got.Role != domain.RoleOwner {
		t.Fatalf("new owner role = %s", got.Role)
	}
	if got, _ := s.Users().GetByID(ctx, owner.ID); got.Role != domain.RoleAdmin {
		t.Fatalf("old owner role = %s, want admin", got.Role)
	}
}
