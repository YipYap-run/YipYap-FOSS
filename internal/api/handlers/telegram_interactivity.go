package handlers

import (
	"bytes"
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
	botToken    string
}

// NewTelegramInteractivityHandler creates a new TelegramInteractivityHandler.
// secretToken is the secret token configured when registering the Telegram
// webhook. If empty, all requests are rejected (fail closed).
// botToken is the Telegram Bot API token used to edit messages after
// ack/resolve actions. If empty, messages will not be updated.
func NewTelegramInteractivityHandler(s store.Store, b bus.Bus, secretToken, botToken string) *TelegramInteractivityHandler {
	return &TelegramInteractivityHandler{store: s, bus: b, secretToken: secretToken, botToken: botToken}
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
	Message *struct {
		MessageID int `json:"message_id"`
		Chat      struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
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

	// Update the original Telegram message: append who acted and remove
	// the inline keyboard so buttons cannot be clicked again.
	cbq := update.CallbackQuery
	if h.botToken != "" && cbq.Message != nil {
		go func() {
			actionStr := "Acknowledged"
			if action == "resolve" {
				actionStr = "Resolved"
			}
			userName := cbq.From.Username
			if userName == "" {
				userName = fmt.Sprintf("user %d", cbq.From.ID)
			}

			updatedText := cbq.Message.Text
			if updatedText != "" {
				updatedText += "\n\n"
			}
			updatedText += fmt.Sprintf("%s by %s", actionStr, userName)

			editURL := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", h.botToken)
			editBody, _ := json.Marshal(map[string]any{
				"chat_id":      cbq.Message.Chat.ID,
				"message_id":   cbq.Message.MessageID,
				"text":         updatedText,
				"parse_mode":   "HTML",
				"reply_markup": map[string]any{"inline_keyboard": []any{}},
			})

			req, err := http.NewRequest("POST", editURL, bytes.NewReader(editBody))
			if err != nil {
				slog.Warn("telegram: failed to build edit request", "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				slog.Warn("telegram: failed to edit message", "error", err)
				return
			}
			_ = resp.Body.Close()
		}()
	}

	w.WriteHeader(http.StatusOK)
}
