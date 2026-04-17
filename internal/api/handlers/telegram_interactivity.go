package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// TelegramInteractivityHandler handles Telegram callback_query updates
// (inline keyboard button clicks for acknowledge/resolve).
type TelegramInteractivityHandler struct {
	store       store.Store
	bus         bus.Bus
	secretToken string
}

// NewTelegramInteractivityHandler creates a new TelegramInteractivityHandler.
// secretToken is the secret token configured when registering the Telegram
// webhook. If empty, all requests are rejected (fail closed).
func NewTelegramInteractivityHandler(s store.Store, b bus.Bus, secretToken string) *TelegramInteractivityHandler {
	return &TelegramInteractivityHandler{store: s, bus: b, secretToken: secretToken}
}

// telegramWebhookUpdate mirrors the relevant fields of a Telegram webhook update.
type telegramWebhookUpdate struct {
	UpdateID      int                           `json:"update_id"`
	CallbackQuery *telegramWebhookCallbackQuery `json:"callback_query"`
}

type telegramWebhookCallbackQuery struct {
	ID   string `json:"id"`
	From struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Data string `json:"data"`
}

// Handle processes POST /webhooks/telegram.
// Telegram sends webhook updates as JSON POST bodies. We only care about
// callback_query updates (inline keyboard button clicks). Always returns 200.
func (h *TelegramInteractivityHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Fail closed if no secret token is configured.
	if h.secretToken == "" {
		slog.Warn("telegram interactivity: no secret token configured, rejecting request")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Verify the secret token header using constant-time comparison.
	incoming := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(incoming), []byte(h.secretToken)) != 1 {
		slog.Warn("telegram interactivity: invalid secret token")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		slog.Warn("telegram interactivity: read body", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	var update telegramWebhookUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		slog.Warn("telegram interactivity: invalid JSON", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Ignore non-callback_query updates.
	if update.CallbackQuery == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	parts := strings.SplitN(update.CallbackQuery.Data, ":", 2)
	if len(parts) != 2 {
		slog.Warn("telegram interactivity: malformed callback_data", "data", update.CallbackQuery.Data)
		w.WriteHeader(http.StatusOK)
		return
	}

	action, alertID := parts[0], parts[1]

	switch action {
	case "ack":
		ackEvt := domain.AckEvent{
			AlertID: alertID,
			UserID:  fmt.Sprintf("%d", update.CallbackQuery.From.ID),
			Channel: "telegram",
		}
		data, err := json.Marshal(ackEvt)
		if err != nil {
			slog.Error("telegram interactivity: marshal ack event", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := h.bus.Publish(r.Context(), "alert.ack", data); err != nil {
			slog.Error("telegram interactivity: publish ack", "error", err)
		}

	case "resolve":
		alert, err := h.store.Alerts().GetByID(r.Context(), alertID)
		if err != nil {
			slog.Warn("telegram interactivity: alert not found", "alert_id", alertID, "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		monitor, err := h.store.Monitors().GetByID(r.Context(), alert.MonitorID)
		if err != nil {
			slog.Warn("telegram interactivity: monitor not found", "monitor_id", alert.MonitorID, "error", err)
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
			slog.Error("telegram interactivity: marshal recover event", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := h.bus.Publish(r.Context(), "alert.recover", data); err != nil {
			slog.Error("telegram interactivity: publish recover", "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}
