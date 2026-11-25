package handlers

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// TelegramInteractivityHandler handles updates sent to the platform
// Telegram bot: /link and /start commands that bind a chat to a channel,
// and callback_query updates from the Acknowledge and Resolve buttons.
type TelegramInteractivityHandler struct {
	store       store.Store
	bus         bus.Bus
	secretToken string
	botToken    string
	botUsername string
	apiBase     string
	client      *http.Client
	linker      *telegramLinker
}

// NewTelegramInteractivityHandler creates a new TelegramInteractivityHandler.
// secretToken is the secret token configured when registering the Telegram
// webhook. If empty, all requests are rejected (fail closed).
// botToken is the Telegram Bot API token used to reply to commands and to
// edit messages after ack/resolve actions. If empty, nothing is sent back.
func NewTelegramInteractivityHandler(s store.Store, b bus.Bus, secretToken, botToken string) *TelegramInteractivityHandler {
	return &TelegramInteractivityHandler{
		store:       s,
		bus:         b,
		secretToken: secretToken,
		botToken:    botToken,
		apiBase:     "https://api.telegram.org",
		client:      &http.Client{Timeout: 10 * time.Second},
		linker:      newTelegramLinker(s),
	}
}

// SetBotUsername sets the bot's public username. Commands addressed to a
// different bot ("/link@otherbot CODE") are then ignored.
func (h *TelegramInteractivityHandler) SetBotUsername(username string) {
	h.botUsername = strings.TrimPrefix(strings.TrimSpace(username), "@")
}

// telegramWebhookUpdate mirrors the relevant fields of a Telegram webhook update.
type telegramWebhookUpdate struct {
	UpdateID      int                           `json:"update_id"`
	CallbackQuery *telegramWebhookCallbackQuery `json:"callback_query"`
	Message       *telegramWebhookMessage       `json:"message"`
}

type telegramWebhookChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type telegramWebhookMessage struct {
	MessageID int                 `json:"message_id"`
	From      telegramWebhookUser `json:"from"`
	Chat      telegramWebhookChat `json:"chat"`
	Text      string              `json:"text"`
}

type telegramWebhookUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type telegramWebhookCallbackQuery struct {
	ID      string              `json:"id"`
	From    telegramWebhookUser `json:"from"`
	Message *struct {
		MessageID int                 `json:"message_id"`
		Chat      telegramWebhookChat `json:"chat"`
		Text      string              `json:"text"`
	} `json:"message"`
	Data string `json:"data"`
}

// parseTelegramCommand splits "/link@yipyapbot ABC" into ("link", "yipyapbot", "ABC").
// ok is false when text is not a command.
func parseTelegramCommand(text string) (cmd, bot, arg string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", "", false
	}
	head, rest, _ := strings.Cut(text, " ")
	head = strings.TrimPrefix(head, "/")
	cmd, bot, _ = strings.Cut(head, "@")
	return strings.ToLower(cmd), bot, strings.TrimSpace(rest), cmd != ""
}

func (h *TelegramInteractivityHandler) call(ctx context.Context, method string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.apiBase+"/bot"+h.botToken+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram %s status %d", method, resp.StatusCode)
	}
	return nil
}

func (h *TelegramInteractivityHandler) sendText(ctx context.Context, chatID int64, text string) {
	if h.botToken == "" {
		return
	}
	if err := h.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text}); err != nil {
		slog.Warn("telegram interactivity: send message", "error", err)
	}
}

// answerCallback stops the button spinner and shows text to the clicker.
func (h *TelegramInteractivityHandler) answerCallback(ctx context.Context, queryID, text string) {
	if h.botToken == "" || queryID == "" {
		return
	}
	payload := map[string]any{"callback_query_id": queryID}
	if text != "" {
		payload["text"] = text
	}
	if err := h.call(ctx, "answerCallbackQuery", payload); err != nil {
		slog.Warn("telegram interactivity: answer callback", "error", err)
	}
}

// handleMessage answers /link and /start. The chat ID is never echoed:
// chats are linked with a code made in YipYap (notif-16).
func (h *TelegramInteractivityHandler) handleMessage(ctx context.Context, msg *telegramWebhookMessage) {
	cmd, bot, arg, ok := parseTelegramCommand(msg.Text)
	if !ok {
		return
	}
	if bot != "" && h.botUsername != "" && !strings.EqualFold(bot, h.botUsername) {
		return // addressed to another bot in the group
	}
	chat := telegramChat{
		ID: msg.Chat.ID, Type: msg.Chat.Type, Title: msg.Chat.Title,
		Username: msg.Chat.Username, FirstName: msg.Chat.FirstName,
	}
	switch cmd {
	case "link":
		if arg == "" {
			h.sendText(ctx, chat.ID, "Send the command YipYap shows you, for example:\n/link ABCD2345EF")
			return
		}
		h.sendText(ctx, chat.ID, h.linker.link(ctx, arg, chat))
	case "start":
		// A t.me deep link (?start=CODE or ?startgroup=CODE) arrives as
		// "/start CODE".
		if arg != "" {
			h.sendText(ctx, chat.ID, h.linker.link(ctx, arg, chat))
			return
		}
		h.sendText(ctx, chat.ID, telegramStartReply)
	case "chatid", "help":
		h.sendText(ctx, chat.ID, telegramStartReply)
	}
}

// Handle processes POST /webhooks/telegram. Always returns 200 once the
// secret token checks out, so Telegram does not retry.
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 16384))
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

	if update.CallbackQuery == nil {
		if update.Message != nil {
			h.handleMessage(r.Context(), update.Message)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	h.handleCallback(r.Context(), update.CallbackQuery)
	w.WriteHeader(http.StatusOK)
}

func telegramActor(from telegramWebhookUser) (actor, label string) {
	switch {
	case from.Username != "":
		label = "@" + from.Username
	case strings.TrimSpace(from.FirstName) != "":
		label = strings.TrimSpace(from.FirstName)
	default:
		label = fmt.Sprintf("user %d", from.ID)
	}
	return "Telegram user " + label, label
}

func (h *TelegramInteractivityHandler) handleCallback(ctx context.Context, cbq *telegramWebhookCallbackQuery) {
	action, alertID, ok := strings.Cut(cbq.Data, ":")
	if !ok || alertID == "" || (action != "ack" && action != "resolve") {
		slog.Warn("telegram interactivity: malformed callback_data", "data", cbq.Data)
		h.answerCallback(ctx, cbq.ID, "")
		return
	}
	if h.store == nil || h.bus == nil {
		slog.Error("telegram interactivity: not wired", "has_store", h.store != nil, "has_bus", h.bus != nil)
		return
	}

	// The click must come from a chat linked to an enabled telegram channel
	// of the alert's org. Anything else reads as not found, so a chat
	// cannot probe or act on another org's alerts (notif-16).
	if cbq.Message == nil {
		slog.Warn("telegram interactivity: callback without message", "alert_id", alertID)
		h.answerCallback(ctx, cbq.ID, "That alert was not found.")
		return
	}
	chatID := cbq.Message.Chat.ID
	alert, err := h.store.Alerts().GetByID(ctx, alertID)
	linked := false
	if err == nil {
		linked, err = orgTelegramChatLinked(ctx, h.store, alert.OrgID, chatID)
		if err != nil {
			slog.Error("telegram interactivity: check chat", "alert_id", alertID, "error", err)
			h.answerCallback(ctx, cbq.ID, "YipYap could not process that click. Try again.")
			return
		}
	}
	if !linked {
		slog.Warn("telegram interactivity: chat not linked to the alert's org", "alert_id", alertID, "chat_id", chatID)
		h.answerCallback(ctx, cbq.ID, "That alert was not found.")
		return
	}

	actor, label := telegramActor(cbq.From)
	if alert.Status == domain.AlertResolved {
		h.answerCallback(ctx, cbq.ID, "Already resolved.")
		h.editMessage(ctx, cbq, "Already resolved")
		return
	}

	switch action {
	case "ack":
		if alert.Status == domain.AlertAcknowledged {
			h.answerCallback(ctx, cbq.ID, "Already acknowledged.")
			return
		}
		data, _ := json.Marshal(domain.AckEvent{AlertID: alert.ID, UserID: actor, Channel: "telegram"})
		// CloudEvent emission for acknowledged.v1 happens in escalation.Engine.handleAck
		// when it consumes this alert.ack message.
		if err := h.bus.PublishAlert(context.WithoutCancel(ctx), "alert.ack", data, ""); err != nil {
			slog.Error("telegram interactivity: publish ack", "error", err)
			h.answerCallback(ctx, cbq.ID, "YipYap could not acknowledge the alert. Try again.")
			return
		}
		h.answerCallback(ctx, cbq.ID, "Acknowledged.")
		h.editMessage(ctx, cbq, "Acknowledged by "+label)

	case "resolve":
		monitor, err := h.store.Monitors().GetByID(ctx, alert.MonitorID)
		if err != nil || monitor.OrgID != alert.OrgID {
			slog.Warn("telegram interactivity: monitor not found", "monitor_id", alert.MonitorID, "error", err)
			h.answerCallback(ctx, cbq.ID, "That alert's monitor was not found.")
			return
		}
		data, _ := json.Marshal(alertTriggerEvent{
			OrgID: alert.OrgID, MonitorID: monitor.ID, MonitorName: monitor.Name,
			Status: domain.StatusUp, CheckedAt: time.Now().UTC(),
			AlertID: alert.ID, ResolvedBy: actor, Channel: "telegram",
		})
		if err := h.bus.PublishAlert(context.WithoutCancel(ctx), "alert.recover", data, ""); err != nil {
			slog.Error("telegram interactivity: publish recover", "error", err)
			h.answerCallback(ctx, cbq.ID, "YipYap could not resolve the alert. Try again.")
			return
		}
		h.answerCallback(ctx, cbq.ID, "Resolved.")
		h.editMessage(ctx, cbq, "Resolved by "+label)
	}
}

// editMessage appends a status line to the clicked message and removes
// the buttons so they cannot be pressed again. The original text comes
// back from Telegram as plain text, so it is escaped for HTML mode.
func (h *TelegramInteractivityHandler) editMessage(ctx context.Context, cbq *telegramWebhookCallbackQuery, line string) {
	if h.botToken == "" || cbq.Message == nil {
		return
	}
	text := telegramEditedText(cbq.Message.Text, line)
	if err := h.call(ctx, "editMessageText", map[string]any{
		"chat_id":      cbq.Message.Chat.ID,
		"message_id":   cbq.Message.MessageID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]any{"inline_keyboard": []any{}},
	}); err != nil {
		slog.Warn("telegram interactivity: edit message", "error", err)
	}
}

// telegramEditedText is the edited message after a button press. Telegram
// returns the message as plain text (the HTML formatting is gone), and the
// edit is sent with parse_mode HTML, so the old text and the status line
// are escaped. Otherwise a monitor named "API <prod>" makes the edit fail
// and the buttons stay live.
func telegramEditedText(oldText, line string) string {
	var b strings.Builder
	if oldText != "" {
		b.WriteString(html.EscapeString(oldText))
		b.WriteString("\n\n")
	}
	b.WriteString(html.EscapeString(line))
	return b.String()
}
