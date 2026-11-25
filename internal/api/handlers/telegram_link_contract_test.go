package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// The pairing UI (web/src/pages/settings/telegram-link-state.mjs) reads
// these exact keys. These tests pin the wire shape of POST and GET
// /telegram/link-codes so a rename or a leaked code shows up here first.

func jsonKeys(t *testing.T, body []byte) (map[string]any, []string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return m, keys
}

func wantKeys(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s keys = %v, want %v", what, got, want)
	}
}

func TestTelegramLinkCodeContract(t *testing.T) {
	env := newTelegramLinkEnv(t)

	rec := env.call(env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes", env.orgA,
		map[string]any{"purpose": "channel", "name": "Ops"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	created, keys := jsonKeys(t, rec.Body.Bytes())
	wantKeys(t, "create", keys, "id", "status", "expires_at", "code", "command", "bot_username", "group_link", "private_link")
	if created["status"] != "pending" || created["bot_username"] != "YipYapBot" {
		t.Fatalf("create body = %v", created)
	}
	code, _ := created["code"].(string)
	if created["command"] != "/link "+code {
		t.Fatalf("command = %v, code %q", created["command"], code)
	}
	exp, err := time.Parse(time.RFC3339, created["expires_at"].(string))
	if err != nil || !exp.Equal(env.now.Add(telegramLinkCodeTTL)) {
		t.Fatalf("expires_at = %v (%v)", created["expires_at"], err)
	}
	id := created["id"].(string)

	get := func() (int, map[string]any, []string) {
		rec := env.call(env.chans.GetTelegramLinkCode, http.MethodGet, "/telegram/link-codes/"+id, env.orgA, nil, map[string]string{"id": id})
		if rec.Code != http.StatusOK {
			return rec.Code, nil, nil
		}
		m, k := jsonKeys(t, rec.Body.Bytes())
		return rec.Code, m, k
	}

	// Pending: no code, no chat yet. The code is shown once, at create.
	_, body, keys := get()
	wantKeys(t, "pending", keys, "id", "status", "expires_at")
	if body["status"] != "pending" || body["expires_at"] != created["expires_at"] {
		t.Fatalf("pending body = %v", body)
	}

	// Linked: the channel and chat title the form shows.
	if reply := env.sendMessage(-1001, "supergroup", "Ops Room", "/link "+code); !strings.Contains(reply, "Linked") {
		t.Fatalf("reply = %q", reply)
	}
	_, body, keys = get()
	wantKeys(t, "linked", keys, "id", "status", "expires_at", "channel_id", "chat_title")
	chans := env.channelsOf(env.orgA)
	if len(chans) != 1 || body["status"] != "linked" || body["chat_title"] != "Ops Room" || body["channel_id"] != chans[0].ID {
		t.Fatalf("linked body = %v", body)
	}
}

// A code nobody sends reads as expired once its window ends, so the UI
// stops polling and offers a new code.
func TestTelegramLinkCodeStatusExpiresWithoutMessage(t *testing.T) {
	env := newTelegramLinkEnv(t)
	code := env.createCode(env.orgA, map[string]any{"name": "Ops"})

	env.now = env.now.Add(telegramLinkCodeTTL - time.Second)
	if _, got := env.codeStatus(env.orgA, code.ID); got.Status != "pending" {
		t.Fatalf("before expiry = %+v", got)
	}
	env.now = env.now.Add(time.Second)
	status, got := env.codeStatus(env.orgA, code.ID)
	if status != http.StatusOK || got.Status != "expired" || got.ChatTitle != "" || got.ChannelID != "" || got.Code != "" {
		t.Fatalf("after expiry = %d %+v", status, got)
	}
}

func TestTelegramLinkCodeStatusUnknownID(t *testing.T) {
	env := newTelegramLinkEnv(t)
	if status, _ := env.codeStatus(env.orgA, "no-such-code"); status != http.StatusNotFound {
		t.Fatalf("unknown id = %d", status)
	}
}
