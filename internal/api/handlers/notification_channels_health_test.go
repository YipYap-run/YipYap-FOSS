package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type healthBody struct {
	ID     string `json:"id"`
	Health struct {
		Status              string     `json:"status"`
		LastError           string     `json:"last_error"`
		LastFailureAt       *time.Time `json:"last_failure_at"`
		ConsecutiveFailures int        `json:"consecutive_failures"`
	} `json:"health"`
}

func getChannelHealth(t *testing.T, url, token string) healthBody {
	t.Helper()
	resp := doJSONReq(t, http.MethodGet, url, token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	var b healthBody
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChannelAPI_ReportsDeliveryHealth(t *testing.T) {
	srv, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, srv, "HealthOrg", "health@test.com", "supersecret123")
	ctx := context.Background()
	user, err := s.Users().GetByEmail(ctx, "health@test.com")
	if err != nil {
		t.Fatal(err)
	}
	ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: user.OrgID, Type: "webhook", Name: "hook", Config: `{"url":"https://example.com/h"}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	base := srv.URL + "/api/v1/notification-channels"

	if got := getChannelHealth(t, base+"/"+ch.ID, token); got.Health.Status != "unknown" {
		t.Fatalf("new channel status = %q, want unknown", got.Health.Status)
	}

	reason := "Delivery failed: the destination returned HTTP 404."
	for range 3 {
		if _, err := s.NotificationChannels().RecordDeliveryFailure(ctx, ch.ID, time.Now().UTC(), reason); err != nil {
			t.Fatal(err)
		}
	}
	got := getChannelHealth(t, base+"/"+ch.ID, token)
	if got.Health.Status != "failing" || got.Health.LastError != reason || got.Health.ConsecutiveFailures != 3 || got.Health.LastFailureAt == nil {
		t.Fatalf("get health = %+v", got.Health)
	}

	resp := doJSONReq(t, http.MethodGet, base, token, nil)
	var list []healthBody
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	if len(list) != 1 || list[0].Health.Status != "failing" || list[0].Health.ConsecutiveFailures != 3 {
		t.Fatalf("list health = %+v", list)
	}

	// A test send that gets through clears the failing state.
	tr := doJSONReq(t, http.MethodPost, base+"/"+ch.ID+"/test", token, nil)
	_ = tr.Body.Close()
	if tr.StatusCode != http.StatusOK {
		t.Fatalf("test send: %d", tr.StatusCode)
	}
	if got := getChannelHealth(t, base+"/"+ch.ID, token); got.Health.Status != "ok" || got.Health.ConsecutiveFailures != 0 || got.Health.LastError != "" {
		t.Fatalf("health after test send = %+v", got.Health)
	}
}
