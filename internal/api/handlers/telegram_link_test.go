package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type telegramLinkEnv struct {
	t      *testing.T
	store  store.Store
	chans  *NotificationChannelHandler
	bot    *TelegramInteractivityHandler
	now    time.Time
	orgA   *domain.Org
	orgB   *domain.Org
	mu     sync.Mutex
	sent   []map[string]any
	method []string
}

func newTelegramLinkEnv(t *testing.T) *telegramLinkEnv {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	env := &telegramLinkEnv{t: t, store: s, now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}
	for i, name := range []string{"Acme", "Globex"} {
		org := &domain.Org{ID: uuid.New().String(), Name: name, Slug: fmt.Sprintf("org-%d-%s", i, uuid.New().String()[:6]), Plan: domain.OrgPlan("pro")}
		if err := s.Orgs().Create(context.Background(), org); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			env.orgA = org
		} else {
			env.orgB = org
		}
	}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		env.mu.Lock()
		env.sent = append(env.sent, got)
		env.method = append(env.method, r.URL.Path)
		env.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(api.Close)

	env.chans = NewNotificationChannelHandler(s, nil)
	env.chans.SetTelegramBot("@YipYapBot", true)
	env.chans.now = func() time.Time { return env.now }
	env.bot = NewTelegramInteractivityHandler(s, nil, "secret", "bot-token")
	env.bot.SetBotUsername("YipYapBot")
	env.bot.apiBase = api.URL
	env.bot.client = api.Client()
	env.bot.linker.now = func() time.Time { return env.now }
	return env
}

func (e *telegramLinkEnv) call(fn http.HandlerFunc, method, path string, org *domain.Org, body any, params map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	ctx := middleware.SetClaims(req.Context(), &auth.Claims{UserID: "user-1", OrgID: org.ID, Role: "admin"})
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

func (e *telegramLinkEnv) createCode(org *domain.Org, body map[string]any) telegramLinkCodeResponse {
	e.t.Helper()
	rec := e.call(e.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes", org, body, nil)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create code: %d %s", rec.Code, rec.Body.String())
	}
	var resp telegramLinkCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *telegramLinkEnv) codeStatus(org *domain.Org, id string) (int, telegramLinkCodeResponse) {
	e.t.Helper()
	rec := e.call(e.chans.GetTelegramLinkCode, http.MethodGet, "/telegram/link-codes/"+id, org, nil, map[string]string{"id": id})
	var resp telegramLinkCodeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

// sendMessage posts a text message from chatID to the bot webhook and
// returns the bot's reply text ("" when the bot said nothing).
func (e *telegramLinkEnv) sendMessage(chatID int64, chatType, title, text string) string {
	e.t.Helper()
	e.mu.Lock()
	before := len(e.sent)
	e.mu.Unlock()
	update := map[string]any{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 2,
			"from":       map[string]any{"id": 3, "username": "jane"},
			"chat":       map[string]any{"id": chatID, "type": chatType, "title": title},
			"text":       text,
		},
	}
	body, _ := json.Marshal(update)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/telegram", bytes.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	rec := httptest.NewRecorder()
	e.bot.Handle(rec, req)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("webhook status %d", rec.Code)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sent) == before {
		return ""
	}
	last := e.sent[len(e.sent)-1]
	if last["chat_id"] != float64(chatID) {
		e.t.Fatalf("reply went to chat %v, want %d", last["chat_id"], chatID)
	}
	text, _ = last["text"].(string)
	return text
}

func (e *telegramLinkEnv) channelsOf(org *domain.Org) []*domain.NotificationChannel {
	e.t.Helper()
	chans, err := e.store.NotificationChannels().ListByOrg(context.Background(), org.ID, store.ListParams{})
	if err != nil {
		e.t.Fatal(err)
	}
	return chans
}

func TestTelegramLinkCreatesChannelWithChatTitle(t *testing.T) {
	env := newTelegramLinkEnv(t)
	code := env.createCode(env.orgA, map[string]any{"name": "Ops Telegram"})
	if len(code.Code) != telegramLinkCodeLen || code.Command != "/link@YipYapBot "+code.Code || code.Status != "pending" {
		t.Fatalf("code response = %+v", code)
	}
	if code.GroupLink != "https://t.me/YipYapBot?startgroup="+code.Code || code.PrivateLink != "https://t.me/YipYapBot?start="+code.Code {
		t.Fatalf("deep links = %q %q", code.GroupLink, code.PrivateLink)
	}
	if !code.ExpiresAt.Equal(env.now.Add(15 * time.Minute)) {
		t.Fatalf("expires_at = %v", code.ExpiresAt)
	}

	// The bot accepts /link@ourbot with a lower-case, dashed code.
	typed := strings.ToLower(code.Code[:5] + "-" + code.Code[5:])
	reply := env.sendMessage(-1001, "supergroup", "Ops Room", "/link@YipYapBot "+typed)
	if !strings.Contains(reply, "Linked") || !strings.Contains(reply, "Acme") || !strings.Contains(reply, "Ops Telegram") {
		t.Fatalf("reply = %q", reply)
	}
	chans := env.channelsOf(env.orgA)
	if len(chans) != 1 {
		t.Fatalf("channels = %d", len(chans))
	}
	ch := chans[0]
	cfg, _ := parseTelegramConfig(ch.Config)
	if ch.Type != "telegram" || ch.Name != "Ops Telegram" || !ch.Enabled || cfg.ChatID != "-1001" || cfg.ChatTitle != "Ops Room" {
		t.Fatalf("channel = %+v cfg %+v", ch, cfg)
	}

	status, got := env.codeStatus(env.orgA, code.ID)
	if status != http.StatusOK || got.Status != "linked" || got.ChannelID != ch.ID || got.ChatTitle != "Ops Room" || got.Code != "" {
		t.Fatalf("status = %d %+v", status, got)
	}
	// Another org cannot read the code.
	if status, _ := env.codeStatus(env.orgB, code.ID); status != http.StatusNotFound {
		t.Fatalf("foreign status read = %d", status)
	}
}

func TestTelegramLinkCodeIsSingleUse(t *testing.T) {
	env := newTelegramLinkEnv(t)
	code := env.createCode(env.orgA, map[string]any{"name": "Ops"})
	if reply := env.sendMessage(-1001, "group", "First", code.Command); !strings.Contains(reply, "Linked") {
		t.Fatalf("first reply = %q", reply)
	}
	reply := env.sendMessage(-2002, "group", "Second", "/link "+code.Code)
	if !strings.Contains(reply, "already used") {
		t.Fatalf("reuse reply = %q", reply)
	}
	chans := env.channelsOf(env.orgA)
	if len(chans) != 1 {
		t.Fatalf("channels = %d", len(chans))
	}
	if cfg, _ := parseTelegramConfig(chans[0].Config); cfg.ChatID != "-1001" {
		t.Fatalf("reused code rebound the chat: %+v", cfg)
	}
}

func TestTelegramLinkCodeExpires(t *testing.T) {
	env := newTelegramLinkEnv(t)
	code := env.createCode(env.orgA, map[string]any{"name": "Ops"})
	env.now = env.now.Add(15 * time.Minute)
	reply := env.sendMessage(-1001, "group", "Ops", "/link "+code.Code)
	if !strings.Contains(reply, "expired") {
		t.Fatalf("reply = %q", reply)
	}
	if n := len(env.channelsOf(env.orgA)); n != 0 {
		t.Fatalf("channels = %d", n)
	}
	if _, got := env.codeStatus(env.orgA, code.ID); got.Status != "expired" {
		t.Fatalf("status = %+v", got)
	}
}

func TestTelegramLinkRelinksExistingChannel(t *testing.T) {
	env := newTelegramLinkEnv(t)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "telegram", Name: "Pager",
		Config: `{"chat_id":"-555"}`, Enabled: true}
	if err := env.store.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	code := env.createCode(env.orgA, map[string]any{"channel_id": ch.ID})
	// A t.me deep link arrives as /start CODE.
	reply := env.sendMessage(-777, "supergroup", "New Room", "/start "+code.Code)
	if !strings.Contains(reply, `"Pager"`) || !strings.Contains(reply, "Acme") {
		t.Fatalf("reply = %q", reply)
	}
	got, _ := env.store.NotificationChannels().GetByID(context.Background(), ch.ID)
	if cfg, _ := parseTelegramConfig(got.Config); cfg.ChatID != "-777" || cfg.ChatTitle != "New Room" {
		t.Fatalf("relinked cfg = %+v", cfg)
	}
	if n := len(env.channelsOf(env.orgA)); n != 1 {
		t.Fatalf("relink created a channel: %d", n)
	}
}

func TestTelegramLinkCodeWrongOrg(t *testing.T) {
	env := newTelegramLinkEnv(t)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "telegram", Name: "A",
		Config: `{}`, Enabled: true}
	if err := env.store.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	// Org B cannot make a code for org A's channel.
	rec := env.call(env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes", env.orgB,
		map[string]any{"channel_id": ch.ID}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign channel code: %d %s", rec.Code, rec.Body.String())
	}
	// A stored code whose subject is in another org (tampered row) binds
	// nothing.
	raw := "WRONGORG23"
	if err := telegramLinkCodes(env.store).Create(context.Background(), &domain.TelegramLinkCode{
		ID: uuid.New().String(), CodeHash: hashTelegramLinkCode(raw), OrgID: env.orgB.ID, CreatedBy: "u",
		Purpose: domain.TelegramLinkPurposeChannel, SubjectID: ch.ID,
		ExpiresAt: env.now.Add(time.Minute), CreatedAt: env.now,
	}); err != nil {
		t.Fatal(err)
	}
	reply := env.sendMessage(-42, "group", "X", "/link "+raw)
	if !strings.Contains(reply, "no longer exists") {
		t.Fatalf("reply = %q", reply)
	}
	got, _ := env.store.NotificationChannels().GetByID(context.Background(), ch.ID)
	if cfg, _ := parseTelegramConfig(got.Config); cfg.ChatID != "" {
		t.Fatalf("org A channel bound by org B code: %+v", cfg)
	}
}

func TestTelegramLinkCodeRequests(t *testing.T) {
	env := newTelegramLinkEnv(t)
	other := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "webhook", Name: "W",
		Config: `{"url":"https://example.com"}`, Enabled: true}
	own := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "telegram", Name: "Own bot",
		Config: `{"bot_token":"123:abc","chat_id":"-9"}`, Enabled: true}
	for _, ch := range []*domain.NotificationChannel{other, own} {
		if err := env.store.NotificationChannels().Create(context.Background(), ch); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing name", map[string]any{}, http.StatusBadRequest},
		{"long name", map[string]any{"name": strings.Repeat("x", 101)}, http.StatusBadRequest},
		{"not telegram", map[string]any{"channel_id": other.ID}, http.StatusBadRequest},
		{"own bot", map[string]any{"channel_id": own.ID}, http.StatusBadRequest},
		{"unknown channel", map[string]any{"channel_id": "nope"}, http.StatusNotFound},
		{"contact method not here yet", map[string]any{"purpose": "contact_method"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := env.call(env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes", env.orgA, tc.body, nil)
		if rec.Code != tc.want {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}

	env.chans.SetTelegramBot("", false)
	rec := env.call(env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes", env.orgA, map[string]any{"name": "x"}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("linking disabled: %d", rec.Code)
	}
	env.chans.SetTelegramBot("YipYapBot", true)

	for i := 0; i < telegramLinkCodesPerHour; i++ {
		env.createCode(env.orgA, map[string]any{"name": "x"})
	}
	rec = env.call(env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes", env.orgA, map[string]any{"name": "x"}, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", rec.Code)
	}
}

func TestTelegramBotCommands(t *testing.T) {
	env := newTelegramLinkEnv(t)

	reply := env.sendMessage(-1001234567890, "group", "Ops", "/start")
	if !strings.Contains(reply, "/link") || strings.Contains(reply, "1001234567890") {
		t.Fatalf("/start reply = %q", reply)
	}
	if reply := env.sendMessage(-1001234567890, "group", "Ops", "/chatid"); strings.Contains(reply, "1001234567890") {
		t.Fatalf("/chatid exposed the chat id: %q", reply)
	}
	if reply := env.sendMessage(-1, "group", "Ops", "/link"); !strings.Contains(reply, "/link ") {
		t.Fatalf("bare /link reply = %q", reply)
	}
	// Commands for another bot in the group are not ours to answer.
	if reply := env.sendMessage(-1, "group", "Ops", "/link@OtherBot ABCDEFGH23"); reply != "" {
		t.Fatalf("answered another bot's command: %q", reply)
	}
	if reply := env.sendMessage(-1, "group", "Ops", "hello"); reply != "" {
		t.Fatalf("answered plain text: %q", reply)
	}

	// Bad codes are throttled per chat.
	for i := 0; i < telegramLinkFailsPerChat; i++ {
		if reply := env.sendMessage(-5, "group", "Ops", "/link ZZZZZZZZZZ"); !strings.Contains(reply, "not valid") {
			t.Fatalf("bad code reply = %q", reply)
		}
	}
	code := env.createCode(env.orgA, map[string]any{"name": "Ops"})
	if reply := env.sendMessage(-5, "group", "Ops", "/link "+code.Code); !strings.Contains(reply, "Too many") {
		t.Fatalf("throttled reply = %q", reply)
	}
	// The code was not consumed by the throttled attempt.
	if reply := env.sendMessage(-6, "private", "", "/link "+code.Code); !strings.Contains(reply, "Linked") {
		t.Fatalf("other chat reply = %q", reply)
	}
}

func TestTelegramChannelRejectsRawChatID(t *testing.T) {
	env := newTelegramLinkEnv(t)
	create := func(cfg string) *httptest.ResponseRecorder {
		return env.call(env.chans.Create, http.MethodPost, "/notification-channels", env.orgA,
			map[string]any{"name": "T", "type": "telegram", "enabled": true, "config": json.RawMessage(cfg)}, nil)
	}
	if rec := create(`{"chat_id":"-100"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("raw chat_id: %d %s", rec.Code, rec.Body.String())
	}
	if rec := create(`{"chat_id":-100}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("numeric chat_id: %d", rec.Code)
	}
	// An unlinked channel can be made, then linked with a code. Any
	// chat_title the client sends is dropped.
	rec := create(`{"chat_title":"spoofed"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pending channel: %d %s", rec.Code, rec.Body.String())
	}
	var made channelResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &made)
	if strings.Contains(string(made.Config), "spoofed") {
		t.Fatalf("client chat_title kept: %s", made.Config)
	}
	// An unlinked channel cannot be tested.
	if rec := env.call(env.chans.Test, http.MethodPost, "/x", env.orgA, nil, map[string]string{"id": made.ID}); rec.Code != http.StatusConflict {
		t.Fatalf("test unlinked: %d", rec.Code)
	}
	// Own bot channels keep their chat_id: the org owns the bot.
	if rec := create(`{"bot_token":"123:abc","chat_id":"-100"}`); rec.Code != http.StatusCreated {
		t.Fatalf("own bot: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTelegramChannelUpdateKeepsLinkedChat(t *testing.T) {
	env := newTelegramLinkEnv(t)
	linked := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "telegram", Name: "L",
		Config: `{"chat_id":"-100","chat_title":"Ops"}`, Enabled: true}
	own := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "telegram", Name: "O",
		Config: `{"bot_token":"123:abc","chat_id":"-999"}`, Enabled: true}
	for _, ch := range []*domain.NotificationChannel{linked, own} {
		if err := env.store.NotificationChannels().Create(context.Background(), ch); err != nil {
			t.Fatal(err)
		}
	}
	update := func(id, cfg string) *httptest.ResponseRecorder {
		return env.call(env.chans.Update, http.MethodPatch, "/notification-channels/"+id, env.orgA,
			map[string]any{"name": "renamed", "config": json.RawMessage(cfg)}, map[string]string{"id": id})
	}
	// Echoing the stored chat back (what the UI does) is fine.
	if rec := update(linked.ID, `{"chat_id":"-100","chat_title":"Ops"}`); rec.Code != http.StatusOK {
		t.Fatalf("echo: %d %s", rec.Code, rec.Body.String())
	}
	// Leaving it out keeps it.
	if rec := update(linked.ID, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("omit: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.NotificationChannels().GetByID(context.Background(), linked.ID)
	if cfg, _ := parseTelegramConfig(got.Config); cfg.ChatID != "-100" || cfg.ChatTitle != "Ops" {
		t.Fatalf("linked chat lost: %+v", cfg)
	}
	// Pointing it at another chat is refused.
	if rec := update(linked.ID, `{"chat_id":"-200"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("retarget: %d", rec.Code)
	}
	// Dropping the own bot token cannot carry a typed chat_id over to the
	// platform bot.
	if rec := update(own.ID, `{"chat_id":"-999"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("own bot to platform with chat: %d %s", rec.Code, rec.Body.String())
	}
	if rec := update(own.ID, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("own bot to platform: %d %s", rec.Code, rec.Body.String())
	}
	got, _ = env.store.NotificationChannels().GetByID(context.Background(), own.ID)
	if cfg, _ := parseTelegramConfig(got.Config); cfg.ChatID != "" || cfg.BotToken != "" {
		t.Fatalf("own bot chat carried over: %+v", cfg)
	}
}
