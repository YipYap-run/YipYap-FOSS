package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type usedByBody struct {
	ID      string                    `json:"id"`
	Enabled bool                      `json:"enabled"`
	UsedBy  []domain.ChannelPolicyRef `json:"used_by"`
	Error   string                    `json:"error"`
	// Raw keeps the used_by bytes so the test can tell [] from null.
	Raw map[string]json.RawMessage `json:"-"`
}

func decodeUsedBy(t *testing.T, resp *http.Response) usedByBody {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw)
	var out usedByBody
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	out.Raw = raw
	return out
}

func newTestChannel(t *testing.T, s store.Store, orgID, name string) *domain.NotificationChannel {
	t.Helper()
	ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: orgID, Type: "webhook", Name: name, Config: `{"url":"https://example.com/h"}`, Enabled: true}
	if err := s.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	return ch
}

func newTestPolicy(t *testing.T, s store.Store, orgID, name string, stepChannels ...[]string) *domain.EscalationPolicy {
	t.Helper()
	ctx := context.Background()
	p := &domain.EscalationPolicy{ID: uuid.NewString(), OrgID: orgID, Name: name}
	if err := s.EscalationPolicies().Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	var steps []domain.EscalationStep
	targets := map[string][]domain.StepTarget{}
	for i, chans := range stepChannels {
		st := domain.EscalationStep{ID: uuid.NewString(), PolicyID: p.ID, Position: i}
		steps = append(steps, st)
		for _, c := range chans {
			targets[st.ID] = append(targets[st.ID], domain.StepTarget{ID: uuid.NewString(), StepID: st.ID, TargetType: domain.TargetChannel, ChannelID: c})
		}
	}
	if err := s.EscalationPolicies().ReplaceSteps(ctx, p.ID, steps, targets); err != nil {
		t.Fatal(err)
	}
	return p
}

// A channel that escalation policies use reports them in used_by on GET,
// list and update; deleting it is refused with 409 and the same list
// until it is removed from every policy, then succeeds with 204.
// References from another org neither show up nor block the delete.
func TestChannelAPI_UsedByBlocksDelete(t *testing.T) {
	srv, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, srv, "UsedByOrg", "usedby@test.com", "supersecret123")
	_ = ceRegisterAndGetToken(t, srv, "OtherOrg", "other-usedby@test.com", "supersecret123")
	ctx := context.Background()
	user, err := s.Users().GetByEmail(ctx, "usedby@test.com")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Users().GetByEmail(ctx, "other-usedby@test.com")
	if err != nil {
		t.Fatal(err)
	}

	used := newTestChannel(t, s, user.OrgID, "pager")
	free := newTestChannel(t, s, user.OrgID, "spare")
	// Steps 1 and 3 use the channel (step 3 twice); step 2 does not.
	primary := newTestPolicy(t, s, user.OrgID, "Primary", []string{used.ID}, []string{free.ID + "-gone"}, []string{used.ID, used.ID})
	backup := newTestPolicy(t, s, user.OrgID, "Backup", []string{used.ID})
	// Another org pointing a step at this org's spare channel.
	newTestPolicy(t, s, other.OrgID, "Foreign", []string{free.ID})

	base := srv.URL + "/api/v1/notification-channels"
	want := []domain.ChannelPolicyRef{
		{PolicyID: backup.ID, PolicyName: "Backup", Steps: []int{1}},
		{PolicyID: primary.ID, PolicyName: "Primary", Steps: []int{1, 3}},
	}

	got := decodeUsedBy(t, doJSONReq(t, http.MethodGet, base+"/"+used.ID, token, nil))
	if !reflect.DeepEqual(got.UsedBy, want) {
		t.Fatalf("GET used_by = %+v, want %+v", got.UsedBy, want)
	}
	got = decodeUsedBy(t, doJSONReq(t, http.MethodGet, base+"/"+free.ID, token, nil))
	if string(got.Raw["used_by"]) != "[]" {
		t.Fatalf("unused channel used_by = %s, want []", got.Raw["used_by"])
	}

	resp := doJSONReq(t, http.MethodGet, base, token, nil)
	var list []usedByBody
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	if len(list) != 2 {
		t.Fatalf("list len = %d", len(list))
	}
	for _, c := range list {
		switch c.ID {
		case used.ID:
			if !reflect.DeepEqual(c.UsedBy, want) {
				t.Fatalf("list used_by = %+v", c.UsedBy)
			}
		case free.ID:
			if c.UsedBy == nil || len(c.UsedBy) != 0 {
				t.Fatalf("list unused used_by = %+v", c.UsedBy)
			}
		}
	}

	// Disabling is allowed and names the policies it affects.
	got = decodeUsedBy(t, doJSONReq(t, http.MethodPatch, base+"/"+used.ID, token, map[string]any{"enabled": false}))
	if got.Enabled || !reflect.DeepEqual(got.UsedBy, want) {
		t.Fatalf("PATCH enabled=false: %+v", got)
	}

	resp = doJSONReq(t, http.MethodDelete, base+"/"+used.ID, token, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE in-use channel: %d, want 409", resp.StatusCode)
	}
	got = decodeUsedBy(t, resp)
	if got.Error == "" || !reflect.DeepEqual(got.UsedBy, want) {
		t.Fatalf("409 body = %+v", got)
	}
	if _, err := s.NotificationChannels().GetByID(ctx, used.ID); err != nil {
		t.Fatalf("channel gone after refused delete: %v", err)
	}

	// Removing it from Backup through the API leaves only Primary.
	esc := srv.URL + "/api/v1/escalation-policies/"
	r := doJSONReq(t, http.MethodPut, esc+backup.ID+"/steps", token, map[string]any{"steps": []any{}})
	_ = r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("replace Backup steps: %d", r.StatusCode)
	}
	resp = doJSONReq(t, http.MethodDelete, base+"/"+used.ID, token, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE still used by Primary: %d", resp.StatusCode)
	}
	if got = decodeUsedBy(t, resp); !reflect.DeepEqual(got.UsedBy, want[1:]) {
		t.Fatalf("409 after Backup removed: %+v", got.UsedBy)
	}

	r = doJSONReq(t, http.MethodPut, esc+primary.ID+"/steps", token, map[string]any{"steps": []any{
		map[string]any{"wait_seconds": 0, "targets": []any{map[string]any{"target_type": "channel", "channel_id": free.ID}}},
	}})
	_ = r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("replace Primary steps: %d", r.StatusCode)
	}
	resp = doJSONReq(t, http.MethodDelete, base+"/"+used.ID, token, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE after removal: %d, want 204", resp.StatusCode)
	}
	resp = doJSONReq(t, http.MethodDelete, base+"/"+used.ID, token, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second DELETE: %d, want 404", resp.StatusCode)
	}

	// spare is now used by Primary in this org, so it is blocked; once
	// Primary drops it only the foreign org's reference is left, which
	// does not count.
	resp = doJSONReq(t, http.MethodDelete, base+"/"+free.ID, token, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE spare used by Primary: %d", resp.StatusCode)
	}
	r = doJSONReq(t, http.MethodPut, esc+primary.ID+"/steps", token, map[string]any{"steps": []any{}})
	_ = r.Body.Close()
	resp = doJSONReq(t, http.MethodDelete, base+"/"+free.ID, token, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE spare with only a foreign reference: %d, want 204", resp.StatusCode)
	}
}
