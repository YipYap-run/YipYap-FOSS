package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// SlackInteractivityHandler handles Slack interactive message callbacks
// (button clicks for acknowledge/resolve).
type SlackInteractivityHandler struct {
	store         store.Store
	bus           bus.Bus
	signingSecret string
}

// NewSlackInteractivityHandler creates a new SlackInteractivityHandler.
// signingSecret is the Slack app's signing secret used to verify request
// authenticity. If empty, all requests are rejected (fail closed).
func NewSlackInteractivityHandler(s store.Store, b bus.Bus, signingSecret string) *SlackInteractivityHandler {
	return &SlackInteractivityHandler{store: s, bus: b, signingSecret: signingSecret}
}

// slackActionPayload mirrors the relevant fields of a Slack block_actions
// interaction callback.
type slackActionPayload struct {
	Type string `json:"type"`
	User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// Handle processes POST /integrations/slack/actions.
// Slack sends interactivity payloads as form-encoded POST with a "payload"
// field containing JSON. We must respond within 3 seconds with 200 OK.
func (h *SlackInteractivityHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Fail closed if no signing secret is configured.
	if h.signingSecret == "" {
		slog.Warn("slack interactivity: no signing secret configured, rejecting request")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Read raw body so we can verify the signature and also parse form values.
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		slog.Warn("slack interactivity: read body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Restore body for form parsing.
	r.Body = io.NopCloser(bytes.NewReader(rawBody))

	// Verify timestamp to prevent replay attacks.
	tsStr := r.Header.Get("X-Slack-Request-Timestamp")
	if tsStr == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if time.Since(time.Unix(ts, 0)).Abs() > 5*time.Minute {
		slog.Warn("slack interactivity: request timestamp too old")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Verify HMAC-SHA256 signature.
	sigHeader := r.Header.Get("X-Slack-Signature")
	if sigHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	mac := hmac.New(sha256.New, []byte(h.signingSecret))
	mac.Write([]byte("v0:" + tsStr + ":"))
	mac.Write(rawBody)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sigHeader)) {
		slog.Warn("slack interactivity: signature mismatch")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	payloadStr := r.FormValue("payload")
	if payloadStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var payload slackActionPayload
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		slog.Warn("slack interactivity: invalid payload", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if len(payload.Actions) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	action := payload.Actions[0]
	alertID := action.Value

	switch action.ActionID {
	case "ack_alert":
		ackEvt := domain.AckEvent{
			AlertID: alertID,
			UserID:  payload.User.ID,
			Channel: "slack",
		}
		data, err := json.Marshal(ackEvt)
		if err != nil {
			slog.Error("slack interactivity: marshal ack event", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := h.bus.Publish(r.Context(), "alert.ack", data); err != nil {
			slog.Error("slack interactivity: publish ack", "error", err)
		}

	case "resolve_alert":
		alert, err := h.store.Alerts().GetByID(r.Context(), alertID)
		if err != nil {
			slog.Warn("slack interactivity: alert not found", "alert_id", alertID, "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		monitor, err := h.store.Monitors().GetByID(r.Context(), alert.MonitorID)
		if err != nil {
			slog.Warn("slack interactivity: monitor not found", "monitor_id", alert.MonitorID, "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		evt := alertTriggerEvent{
			MonitorID:   monitor.ID,
			MonitorName: monitor.Name,
			Status:      domain.StatusUp,
			LatencyMS:   0,
			CheckedAt:   time.Now().UTC(),
		}
		data, err := json.Marshal(evt)
		if err != nil {
			slog.Error("slack interactivity: marshal recover event", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := h.bus.Publish(r.Context(), "alert.recover", data); err != nil {
			slog.Error("slack interactivity: publish recover", "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}
