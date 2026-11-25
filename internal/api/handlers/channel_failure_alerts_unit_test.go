package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

func TestChannelFailureAlerts_ReportsEmailEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		h := NewChannelFailureAlertsHandler(nil)
		h.SetEmailEnabled(enabled)
		req := httptest.NewRequest(http.MethodGet, "/org/channel-failure-alerts", nil)
		req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{OrgID: "o", Role: "owner"}))
		w := httptest.NewRecorder()
		h.Get(w, req)
		var out channelFailureAlertsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.EmailEnabled != enabled || out.Threshold != 3 {
			t.Fatalf("enabled=%v: got %+v", enabled, out)
		}
	}
}
