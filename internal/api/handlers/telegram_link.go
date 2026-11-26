package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Telegram chats are bound to channels with a one-time code (notif-16).
// The UI shows "/link CODE"; whoever sends it in a chat with the platform
// bot binds that chat. A raw chat_id is never accepted for the platform
// bot, so one org cannot point its alerts (and Ack buttons) at a chat it
// does not control.

const (
	telegramLinkCodeTTL       = 15 * time.Minute
	telegramLinkCodeLen       = 10
	telegramLinkCodesPerHour  = 20
	telegramLinkMaxNameLen    = 100
	telegramLinkFailWindow    = 10 * time.Minute
	telegramLinkFailsPerChat  = 5
	telegramLinkCodeAlphabet  = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O or 1/I
	telegramLinkStatusPending = "pending"
	telegramLinkStatusLinked  = "linked"
	telegramLinkStatusExpired = "expired"
	telegramLinkStatusFailed  = "failed"
	// A claimed code without a result is still being bound for this long;
	// after that the bind failed and the bot told the chat why.
	telegramLinkBindGrace = 30 * time.Second
)

// newTelegramLinkCode returns a random code of telegramLinkCodeLen symbols
// (50 bits). The alphabet is a power of two, so there is no modulo bias.
func newTelegramLinkCode() (string, error) {
	buf := make([]byte, telegramLinkCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, telegramLinkCodeLen)
	for i, b := range buf {
		out[i] = telegramLinkCodeAlphabet[int(b)%len(telegramLinkCodeAlphabet)]
	}
	return string(out), nil
}

// normalizeTelegramLinkCode upper-cases a typed code and drops spaces and
// dashes, so "abcd-efgh 23" matches "ABCDEFGH23".
func normalizeTelegramLinkCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if r == ' ' || r == '-' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hashTelegramLinkCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func telegramLinkCodes(s store.Store) store.TelegramLinkCodeStore {
	if p, ok := s.(store.TelegramLinkCodeProvider); ok {
		return p.TelegramLinkCodes()
	}
	return nil
}

// telegramConfig is the stored config of a telegram channel. BotToken is
// set only on API-created channels that use their own bot.
type telegramConfig struct {
	BotToken  string
	ChatID    string
	ChatTitle string
}

func parseTelegramConfig(raw string) (telegramConfig, map[string]any) {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &m)
	if m == nil {
		m = map[string]any{}
	}
	return telegramConfig{
		BotToken:  jsonScalarString(m["bot_token"]),
		ChatID:    jsonScalarString(m["chat_id"]),
		ChatTitle: jsonScalarString(m["chat_title"]),
	}, m
}

// jsonScalarString reads a JSON string or number as a string. Older
// channels may hold chat_id as a number.
func jsonScalarString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}

// usesPlatformTelegramBot reports whether a channel sends through the
// YipYap bot (no bot token of its own).
func (c telegramConfig) usesPlatformBot() bool { return c.BotToken == "" }

var errTelegramChatIDDirect = errors.New("telegram chats are linked with a one-time code: create a link code and send /link CODE to the YipYap bot in the chat")

// prepareTelegramCreateConfig enforces the link-code rule on a new
// telegram channel. Platform bot channels start unlinked; chat_title is
// always server-owned.
func prepareTelegramCreateConfig(channelType, raw string) (string, error) {
	if channelType != "telegram" {
		return raw, nil
	}
	cfg, m := parseTelegramConfig(raw)
	delete(m, "chat_title")
	if cfg.usesPlatformBot() {
		if cfg.ChatID != "" {
			return "", errTelegramChatIDDirect
		}
		delete(m, "chat_id")
	}
	out, err := json.Marshal(m)
	return string(out), err
}

// prepareTelegramUpdateConfig keeps chat_id and chat_title server-owned on
// platform bot channels. merged is the new config after secret merging;
// existing is the stored config. A client that echoes the stored values
// back is fine; changing them is refused.
func prepareTelegramUpdateConfig(channelType, merged, existing string) (string, error) {
	if channelType != "telegram" {
		return merged, nil
	}
	next, m := parseTelegramConfig(merged)
	prev, _ := parseTelegramConfig(existing)
	if !next.usesPlatformBot() {
		// Own bot: the org owns the bot, so its chat_id is its own business.
		// The title only describes platform links.
		delete(m, "chat_title")
		out, err := json.Marshal(m)
		return string(out), err
	}
	keepID, keepTitle := "", ""
	if prev.usesPlatformBot() {
		keepID, keepTitle = prev.ChatID, prev.ChatTitle
	}
	if next.ChatID != "" && next.ChatID != keepID {
		return "", errTelegramChatIDDirect
	}
	delete(m, "chat_id")
	delete(m, "chat_title")
	if keepID != "" {
		m["chat_id"] = keepID
	}
	if keepTitle != "" {
		m["chat_title"] = keepTitle
	}
	out, err := json.Marshal(m)
	return string(out), err
}

// telegramChannelUnlinked reports a telegram channel that has no chat to
// send to yet.
func telegramChannelUnlinked(ch *domain.NotificationChannel) bool {
	if ch == nil || ch.Type != "telegram" {
		return false
	}
	cfg, _ := parseTelegramConfig(ch.Config)
	return cfg.ChatID == ""
}

// SetTelegramBot tells the handler whether the platform Telegram bot can
// receive /link commands (bot token and webhook secret configured) and
// its public username for deep links.
func (h *NotificationChannelHandler) SetTelegramBot(username string, linkingEnabled bool) {
	h.telegramBotUsername = strings.TrimPrefix(strings.TrimSpace(username), "@")
	h.telegramLinking = linkingEnabled
}

type telegramLinkCodeResponse struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expires_at"`
	Code        string    `json:"code,omitempty"`
	Command     string    `json:"command,omitempty"`
	BotUsername string    `json:"bot_username,omitempty"`
	GroupLink   string    `json:"group_link,omitempty"`
	PrivateLink string    `json:"private_link,omitempty"`
	ChannelID   string    `json:"channel_id,omitempty"`
	ChatTitle   string    `json:"chat_title,omitempty"`
}

func telegramLinkStatus(c *domain.TelegramLinkCode, now time.Time) string {
	switch {
	case c.UsedAt != nil && c.LinkedSubjectID != "":
		return telegramLinkStatusLinked
	case c.UsedAt != nil && now.Sub(*c.UsedAt) < telegramLinkBindGrace:
		return telegramLinkStatusPending
	case c.UsedAt != nil:
		return telegramLinkStatusFailed
	case !now.Before(c.ExpiresAt):
		return telegramLinkStatusExpired
	}
	return telegramLinkStatusPending
}

func (h *NotificationChannelHandler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now().UTC()
}

// CreateTelegramLinkCode handles POST /telegram/link-codes. Body:
// {"purpose":"channel","channel_id":"..."} to link an existing telegram
// channel, or {"purpose":"channel","name":"Ops"} to create a channel when
// the chat is linked. The code is returned once and never stored in clear.
func (h *NotificationChannelHandler) CreateTelegramLinkCode(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	codes := telegramLinkCodes(h.store)
	if codes == nil || !h.telegramLinking {
		errorResponse(w, http.StatusServiceUnavailable, "telegram linking is not configured on this server")
		return
	}
	var req struct {
		Purpose   string `json:"purpose"`
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Purpose == "" {
		req.Purpose = domain.TelegramLinkPurposeChannel
	}
	if req.Purpose != domain.TelegramLinkPurposeChannel {
		// Per-user contact methods arrive with P23; until then only
		// channels can be linked.
		errorResponse(w, http.StatusBadRequest, "unsupported purpose")
		return
	}
	if !checkChannelPlanGate(h, w, r, "telegram") {
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.ChannelID != "" {
		ch, err := h.store.NotificationChannels().GetByID(r.Context(), req.ChannelID)
		if err != nil || ch.OrgID != claims.OrgID {
			errorResponse(w, http.StatusNotFound, "notification channel not found")
			return
		}
		if ch.Type != "telegram" {
			errorResponse(w, http.StatusBadRequest, "only telegram channels can be linked to a chat")
			return
		}
		if cfg, _ := parseTelegramConfig(ch.Config); !cfg.usesPlatformBot() {
			errorResponse(w, http.StatusBadRequest, "this channel uses its own bot; set its chat_id directly")
			return
		}
		req.Name = ""
	} else {
		if req.Name == "" {
			errorResponse(w, http.StatusBadRequest, "name is required")
			return
		}
		if len([]rune(req.Name)) > telegramLinkMaxNameLen {
			errorResponse(w, http.StatusBadRequest, fmt.Sprintf("name must be at most %d characters", telegramLinkMaxNameLen))
			return
		}
		if err := checkChannelLimit(r.Context(), h.store, claims.OrgID); err != nil {
			errorResponse(w, http.StatusForbidden, err.Error())
			return
		}
	}

	now := h.clock()
	n, err := codes.CountByOrgSince(r.Context(), claims.OrgID, now.Add(-time.Hour))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create link code")
		return
	}
	if n >= telegramLinkCodesPerHour {
		w.Header().Set("Retry-After", "3600")
		errorResponse(w, http.StatusTooManyRequests, "too many link codes this hour; try again later")
		return
	}

	code, err := newTelegramLinkCode()
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create link code")
		return
	}
	row := &domain.TelegramLinkCode{
		ID:          uuid.New().String(),
		CodeHash:    hashTelegramLinkCode(code),
		OrgID:       claims.OrgID,
		CreatedBy:   claims.UserID,
		Purpose:     req.Purpose,
		SubjectID:   req.ChannelID,
		ChannelName: req.Name,
		ExpiresAt:   now.Add(telegramLinkCodeTTL),
		CreatedAt:   now,
	}
	if err := codes.Create(r.Context(), row); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create link code")
		return
	}
	resp := telegramLinkCodeResponse{
		ID:          row.ID,
		Status:      telegramLinkStatusPending,
		ExpiresAt:   row.ExpiresAt,
		Code:        code,
		Command:     "/link " + code,
		BotUsername: h.telegramBotUsername,
	}
	if h.telegramBotUsername != "" {
		// Address the command to our bot. Under Telegram's group privacy
		// mode a bot gets "/cmd@this_bot" reliably, while a bare "/cmd" can
		// go to another bot in the group.
		resp.Command = "/link@" + h.telegramBotUsername + " " + code
		base := "https://t.me/" + url.PathEscape(h.telegramBotUsername)
		resp.GroupLink = base + "?startgroup=" + code
		resp.PrivateLink = base + "?start=" + code
	}
	jsonResponse(w, http.StatusCreated, resp)
}

// GetTelegramLinkCode handles GET /telegram/link-codes/{id}. The UI polls
// it to learn when the chat was linked. The code itself is not returned.
func (h *NotificationChannelHandler) GetTelegramLinkCode(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	codes := telegramLinkCodes(h.store)
	if codes == nil {
		errorResponse(w, http.StatusNotFound, "link code not found")
		return
	}
	row, err := codes.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read link code")
		return
	}
	if row == nil || row.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "link code not found")
		return
	}
	status := telegramLinkStatus(row, h.clock())
	resp := telegramLinkCodeResponse{ID: row.ID, Status: status, ExpiresAt: row.ExpiresAt}
	if status == telegramLinkStatusLinked {
		resp.ChannelID = row.LinkedSubjectID
		resp.ChatTitle = row.LinkedChatTitle
	}
	jsonResponse(w, http.StatusOK, resp)
}

// telegramChat is what the bot knows about the chat a command came from.
type telegramChat struct {
	ID        int64
	Type      string
	Title     string
	Username  string
	FirstName string
}

// displayTitle names the chat for the UI: the group title, or the
// username or first name of a private chat.
func (c telegramChat) displayTitle() string {
	switch {
	case strings.TrimSpace(c.Title) != "":
		return strings.TrimSpace(c.Title)
	case c.Username != "":
		return "@" + c.Username
	case strings.TrimSpace(c.FirstName) != "":
		return strings.TrimSpace(c.FirstName)
	}
	return "Telegram chat"
}

// telegramLinker binds chats to channels when the bot receives a code.
type telegramLinker struct {
	store store.Store
	now   func() time.Time

	mu    sync.Mutex
	fails map[int64][]time.Time
}

func newTelegramLinker(s store.Store) *telegramLinker {
	return &telegramLinker{store: s, now: func() time.Time { return time.Now().UTC() }, fails: map[int64][]time.Time{}}
}

// tooManyFails reports whether a chat sent too many bad codes recently.
// Codes carry 50 bits, so this is defence in depth; it is per replica.
func (l *telegramLinker) tooManyFails(chatID int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.fails[chatID][:0]
	for _, t := range l.fails[chatID] {
		if now.Sub(t) < telegramLinkFailWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, chatID)
	} else {
		l.fails[chatID] = kept
	}
	return len(kept) >= telegramLinkFailsPerChat
}

func (l *telegramLinker) recordFail(chatID int64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[chatID] = append(l.fails[chatID], now)
}

// link binds chat to the target of rawCode and returns the reply text for
// the chat. It never reveals anything about a code it did not accept.
func (l *telegramLinker) link(ctx context.Context, rawCode string, chat telegramChat) string {
	now := l.now()
	code := normalizeTelegramLinkCode(rawCode)
	if code == "" {
		return telegramStartReply
	}
	if l.tooManyFails(chat.ID, now) {
		return "Too many invalid codes from this chat. Wait a few minutes, then try again."
	}
	codes := telegramLinkCodes(l.store)
	if codes == nil {
		return "Linking is not available on this server."
	}
	row, err := codes.GetByHash(ctx, hashTelegramLinkCode(code))
	if err != nil {
		slog.Error("telegram link: read code", "error", err)
		return "YipYap could not check that code. Try again in a minute."
	}
	if row == nil {
		l.recordFail(chat.ID, now)
		return "That code is not valid. Create a new one in YipYap under Settings, Channels."
	}
	if row.UsedAt != nil {
		return "That code was already used. Create a new one in YipYap."
	}
	if !now.Before(row.ExpiresAt) {
		return "That code has expired. Create a new one in YipYap."
	}
	if row.Purpose != domain.TelegramLinkPurposeChannel {
		return "That code cannot be used here."
	}
	claimed, err := codes.Claim(ctx, row.ID, now)
	if err != nil {
		slog.Error("telegram link: claim code", "error", err)
		return "YipYap could not check that code. Try again in a minute."
	}
	if !claimed {
		return "That code was already used or has expired. Create a new one in YipYap."
	}

	orgName := "your YipYap organization"
	if org, err := l.store.Orgs().GetByID(ctx, row.OrgID); err == nil && strings.TrimSpace(org.Name) != "" {
		orgName = strings.TrimSpace(org.Name)
	}

	ch, msg := l.bindChannel(ctx, row, chat)
	if ch == nil {
		return msg
	}
	if err := codes.SetLinked(ctx, row.ID, strconv.FormatInt(chat.ID, 10), chat.displayTitle(), ch.ID); err != nil {
		slog.Error("telegram link: record result", "code_id", row.ID, "error", err)
	}
	slog.Info("telegram link: chat linked", "org_id", row.OrgID, "channel_id", ch.ID, "chat_id", chat.ID)
	return fmt.Sprintf("Linked. YipYap alerts for the %q channel in %s will post in this chat.", ch.Name, orgName)
}

func (l *telegramLinker) bindChannel(ctx context.Context, row *domain.TelegramLinkCode, chat telegramChat) (*domain.NotificationChannel, string) {
	chatID := strconv.FormatInt(chat.ID, 10)
	title := chat.displayTitle()
	if row.SubjectID != "" {
		ch, err := l.store.NotificationChannels().GetByID(ctx, row.SubjectID)
		if err != nil || ch.OrgID != row.OrgID || ch.Type != "telegram" {
			return nil, "The channel this code was made for no longer exists. Create a new code in YipYap."
		}
		cfg, m := parseTelegramConfig(ch.Config)
		if !cfg.usesPlatformBot() {
			return nil, "That channel uses its own bot and cannot be linked here."
		}
		m["chat_id"] = chatID
		m["chat_title"] = title
		out, _ := json.Marshal(m)
		ch.Config = string(out)
		if err := l.store.NotificationChannels().Update(ctx, ch); err != nil {
			slog.Error("telegram link: update channel", "channel_id", ch.ID, "error", err)
			return nil, "YipYap could not save the link. Create a new code and try again."
		}
		return ch, ""
	}

	if err := checkChannelLimit(ctx, l.store, row.OrgID); err != nil {
		return nil, "The organization has reached its channel limit, so no channel was added."
	}
	cfg, _ := json.Marshal(map[string]string{"chat_id": chatID, "chat_title": title})
	name := row.ChannelName
	if name == "" {
		name = "Telegram: " + title
	}
	ch := &domain.NotificationChannel{
		ID:      uuid.New().String(),
		OrgID:   row.OrgID,
		Type:    "telegram",
		Name:    name,
		Config:  string(cfg),
		Enabled: true,
	}
	if err := l.store.NotificationChannels().Create(ctx, ch); err != nil {
		slog.Error("telegram link: create channel", "org_id", row.OrgID, "error", err)
		return nil, "YipYap could not save the link. Create a new code and try again."
	}
	return ch, ""
}

const telegramStartReply = "Hi, this is the YipYap alerts bot.\n\n" +
	"To get alerts in this chat, open YipYap, go to Settings, Channels, add a Telegram channel " +
	"and copy the link command it shows. Then send it here, for example:\n/link ABCD2345EF\n\n" +
	"In a group, add this bot to the group first."

// orgTelegramChatLinked reports whether chatID is the chat of an enabled
// telegram channel in orgID. A button click is only honoured from such a
// chat, so a chat linked to one org cannot act on another org's alerts.
func orgTelegramChatLinked(ctx context.Context, s store.Store, orgID string, chatID int64) (bool, error) {
	want := strconv.FormatInt(chatID, 10)
	const page = 200
	for offset := 0; ; offset += page {
		chans, err := s.NotificationChannels().ListByOrg(ctx, orgID, store.ListParams{Limit: page, Offset: offset})
		if err != nil {
			return false, err
		}
		for _, ch := range chans {
			if ch.Type != "telegram" || !ch.Enabled {
				continue
			}
			if cfg, _ := parseTelegramConfig(ch.Config); cfg.ChatID == want {
				return true, nil
			}
		}
		if len(chans) < page {
			return false, nil
		}
	}
}
