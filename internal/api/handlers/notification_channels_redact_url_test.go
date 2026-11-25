package handlers

import (
	"encoding/json"
	"testing"
)

func TestIsWriteOnlyURL(t *testing.T) {
	cases := []struct {
		url, format string
		want        bool
	}{
		{"https://hooks.slack.com/services/T0/B0/abc", "", true},
		{"https://discord.com/api/webhooks/123/abc", "", true},
		{"https://ptb.discord.com/api/webhooks/123/abc", "", true},
		{"https://acme.webhook.office.com/webhookb2/x", "", true},
		{"https://prod-01.westus.logic.azure.com/workflows/x", "", true},
		{"https://chat.googleapis.com/v1/spaces/AAA/messages", "", true},
		{"https://oapi.dingtalk.com/robot/send", "", true},
		{"https://open.feishu.cn/open-apis/bot/v2/hook/x", "", true},
		{"https://example.com/hook", "slack", true},
		{"https://example.com/hook?token=abc", "", true},
		{"https://user:pass@example.com/hook", "", true},
		{"https://example.com/hooks/a1b2c3d4e5f6a7b8c9d0", "", true},
		{"https://example.com/hooks/3f2504e0-4f89-11d3-9a0c-0305e82c3301", "", true},
		{"https://example.com/abcdefghijklmnopqrstuvwxyzabcdefgh", "", true},
		{"not a url", "", true},
		{"https://example.com/alerts", "", false},
		{"https://example.com/hooks/yipyap-alerts", "", false},
		{"https://api.example.com:8443/v2/incidents", "generic", false},
		{"", "slack", false},
	}
	for _, tc := range cases {
		if got := isWriteOnlyURL(tc.url, tc.format); got != tc.want {
			t.Errorf("isWriteOnlyURL(%q, %q) = %v, want %v", tc.url, tc.format, got, tc.want)
		}
	}
}

func TestRedactConfig_CloudEventSinkWithToken(t *testing.T) {
	out, red := redactConfig("cloudevent_http", `{"sink_url":"https://sink.example.com/in?key=zz9","mode":"binary"}`)
	if red == nil || red.Field != "sink_url" || red.Host != "https://sink.example.com" {
		t.Fatalf("redaction = %+v", red)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	if m["sink_url"] != configSentinel {
		t.Fatalf("sink_url = %v", m["sink_url"])
	}
	merged := mergeConfig("cloudevent_http", out, `{"sink_url":"https://sink.example.com/in?key=zz9"}`)
	_ = json.Unmarshal([]byte(merged), &m)
	if m["sink_url"] != "https://sink.example.com/in?key=zz9" {
		t.Fatalf("merged sink_url = %v", m["sink_url"])
	}
}

func TestMergeHeaders_CaseInsensitiveKeep(t *testing.T) {
	merged := mergeConfig("webhook",
		`{"url":"https://example.com/a","headers":{"authorization":"`+configSentinel+`"}}`,
		`{"url":"https://example.com/a","headers":{"Authorization":"Bearer x"}}`)
	if field := unresolvedSentinel("webhook", []byte(merged)); field != "" {
		t.Fatalf("unresolved %s in %s", field, merged)
	}
}
