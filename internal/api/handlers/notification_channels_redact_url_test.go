package handlers

import "testing"

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

func TestMergeHeaders_CaseInsensitiveKeep(t *testing.T) {
	merged := mergeConfig("webhook",
		`{"url":"https://example.com/a","headers":{"authorization":"`+configSentinel+`"}}`,
		`{"url":"https://example.com/a","headers":{"Authorization":"Bearer x"}}`)
	if field := unresolvedSentinel("webhook", []byte(merged)); field != "" {
		t.Fatalf("unresolved %s in %s", field, merged)
	}
}
