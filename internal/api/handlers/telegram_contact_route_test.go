package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// newTelegramCMFixture is the contact method fixture with the platform bot
// configured and the org on Pro, since Telegram is a paid type on SaaS.
func newTelegramCMFixture(t *testing.T) cmFixture {
	t.Helper()
	f := newCMFixtureOpts(t, func(o *api.ServerOptions) {
		o.TelegramBotToken = "123:test"
		o.TelegramWebhookSecret = "secret"
		o.TelegramBotUsername = "YipYapBot"
	})
	ctx := context.Background()
	u, err := f.s.Users().GetByEmail(ctx, "cm-owner@test.com")
	if err != nil {
		t.Fatal(err)
	}
	org, err := f.s.Orgs().GetByID(ctx, u.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	org.Plan = domain.OrgPlan("pro")
	if err := f.s.Orgs().Update(ctx, org); err != nil {
		t.Fatal(err)
	}
	return f
}

// Through the real router: a plain member may make a code for their own
// contact method, while channel codes keep the channel permission.
func TestTelegramContactCodeRouteForMember(t *testing.T) {
	f := newTelegramCMFixture(t)

	code, out, _ := f.member.do(http.MethodPost, "/telegram/link-codes", map[string]any{"purpose": "contact_method"})
	if code != http.StatusCreated || out["private_link"] == nil || out["group_link"] != nil {
		t.Fatalf("member contact code: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if code, got, _ := f.member.do(http.MethodGet, "/telegram/link-codes/"+id, nil); code != http.StatusOK || got["status"] != "pending" {
		t.Fatalf("member status: %d %v", code, got)
	}
	if code, _, _ := f.owner.do(http.MethodGet, "/telegram/link-codes/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("owner read a member's code: %d", code)
	}
	if code, _, _ := f.other.do(http.MethodGet, "/telegram/link-codes/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("other org read the code: %d", code)
	}

	if code, out, _ := f.member.do(http.MethodPost, "/telegram/link-codes", map[string]any{"purpose": "channel", "name": "Ops"}); code != http.StatusForbidden {
		t.Fatalf("member channel code: %d %v", code, out)
	}
	code, out, _ = f.owner.do(http.MethodPost, "/telegram/link-codes", map[string]any{"name": "Ops"})
	if code != http.StatusCreated {
		t.Fatalf("owner channel code: %d %v", code, out)
	}
	chID, _ := out["id"].(string)
	if code, _, _ := f.member.do(http.MethodGet, "/telegram/link-codes/"+chID, nil); code != http.StatusForbidden {
		t.Fatalf("member read a channel code: %d", code)
	}
}

func TestContactMethodOptionsTelegramLinking(t *testing.T) {
	f := newTelegramCMFixture(t)
	code, out, _ := f.owner.do(http.MethodPost, "/notification-channels", map[string]any{
		"type": "telegram", "name": "Own bot", "config": map[string]any{"bot_token": "999:own", "chat_id": "-5"},
	})
	if code != http.StatusCreated {
		t.Fatalf("own bot channel: %d %v", code, out)
	}
	code, out, _ = f.member.do(http.MethodGet, "/users/me/contact-method-options", nil)
	if code != http.StatusOK || out["telegram_linking"] != true {
		t.Fatalf("options: %d %v", code, out)
	}
	ints, _ := out["integrations"].([]any)
	if len(ints) != 1 || ints[0].(map[string]any)["own_bot"] != true {
		t.Fatalf("integrations = %v", ints)
	}

	plain := newCMFixture(t)
	if code, out, _ := plain.member.do(http.MethodGet, "/users/me/contact-method-options", nil); code != http.StatusOK || out["telegram_linking"] != false {
		t.Fatalf("options without bot: %d %v", code, out)
	}
}
