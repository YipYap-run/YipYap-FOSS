package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// TelegramConfig holds Telegram bot settings.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// Telegram delivers notifications via the Telegram Bot API.
type Telegram struct {
	fallback TelegramConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
	apiBase  string
}

// NewTelegram creates a Telegram notifier.
func NewTelegram(fallback TelegramConfig, decrypt notify.DecryptFunc) *Telegram {
	return &Telegram{
		fallback: fallback,
		decrypt:  decrypt,
		client:   &http.Client{Timeout: 30 * time.Second},
		apiBase:  "https://api.telegram.org",
	}
}

func (t *Telegram) Channel() string     { return "telegram" }
func (t *Telegram) MaxConcurrency() int { return 5 }

func (t *Telegram) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := t.fallback
	if err := resolveConfig(job, t.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve telegram config: %w", err)
	}
	if strings.TrimSpace(cfg.ChatID) == "" {
		// The channel was created but no chat has sent its /link code yet.
		return "", fmt.Errorf("telegram chat not linked: send the channel's /link code to the bot in the chat")
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, cfg.BotToken)
	text := telegramMessageText(job)

	payload := map[string]any{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": "HTML",
		"reply_markup": map[string]any{
			"inline_keyboard": [][]map[string]string{
				{
					{
						"text":          "Acknowledge",
						"callback_data": fmt.Sprintf("ack:%s", job.AlertID),
					},
					{
						"text":          "Resolve",
						"callback_data": fmt.Sprintf("resolve:%s", job.AlertID),
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal telegram payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("telegram POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description"`
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode telegram response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("telegram API error: %s", result.Description)
	}
	return fmt.Sprintf("%d", result.Result.MessageID), nil
}

// telegramMessageText builds the HTML body for parse_mode HTML. Every
// interpolated value is escaped: Telegram rejects the whole message when the
// text holds an unknown tag or a bare '<' or '&', so a monitor named
// "API <prod>" would otherwise never arrive.
func telegramMessageText(job domain.NotificationJob) string {
	esc := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n%s", esc(job.Severity), esc(job.MonitorName), esc(job.Message))
	if href := telegramHref(job.RunbookURL); href != "" {
		fmt.Fprintf(&b, "\n\xf0\x9f\x93\x8b <a href=\"%s\">Runbook</a>", href)
	}
	if job.ServiceName != "" {
		fmt.Fprintf(&b, "\n<b>Service:</b> %s", esc(job.ServiceName))
	}
	if len(job.DependencyStatus) > 0 {
		var deps []struct {
			ServiceName  string `json:"service_name"`
			Relationship string `json:"relationship"`
			IsAlerting   bool   `json:"is_alerting"`
		}
		if err := json.Unmarshal(job.DependencyStatus, &deps); err == nil && len(deps) > 0 {
			b.WriteString("\n<b>Dependencies:</b>")
			for _, dep := range deps {
				icon := "\xe2\x9c\x85"
				if dep.IsAlerting {
					icon = "\xf0\x9f\x94\xb4"
				}
				fmt.Fprintf(&b, "\n%s %s (%s)", icon, esc(dep.ServiceName), esc(dep.Relationship))
			}
		}
	}
	if len(job.ContextLinks) > 0 {
		var links []struct {
			Label string `json:"label"`
			URL   string `json:"url"`
		}
		if err := json.Unmarshal(job.ContextLinks, &links); err == nil && len(links) > 0 {
			var linkParts []string
			for _, link := range links {
				label := esc(link.Label)
				if label == "" {
					label = esc(link.URL)
				}
				if href := telegramHref(link.URL); href != "" {
					linkParts = append(linkParts, fmt.Sprintf("<a href=\"%s\">%s</a>", href, label))
				} else if label != "" {
					linkParts = append(linkParts, label)
				}
			}
			if len(linkParts) > 0 {
				b.WriteString("\n<b>Quick Links:</b> " + strings.Join(linkParts, " | "))
			}
		}
	}
	return b.String()
}

// telegramHref returns an escaped href for an http(s) URL, or "" for
// anything else (empty, relative, javascript: and similar).
func telegramHref(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return html.EscapeString(raw)
}

// telegramUpdate mirrors the relevant fields of a Telegram webhook update.
type telegramUpdate struct {
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramCallbackQuery struct {
	ID   string `json:"id"`
	From struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Data string `json:"data"`
}

// ParseAck implements notify.AckListener. It extracts an AckEvent from a
// Telegram callback_query webhook update.
func (t *Telegram) ParseAck(r *http.Request) (*domain.AckEvent, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("read telegram body: %w", err)
	}

	var update telegramUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		return nil, fmt.Errorf("unmarshal telegram update: %w", err)
	}

	if update.CallbackQuery == nil {
		return nil, fmt.Errorf("no callback_query in update")
	}

	parts := strings.SplitN(update.CallbackQuery.Data, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed callback_data: %s", update.CallbackQuery.Data)
	}

	action, alertID := parts[0], parts[1]
	channel := "telegram"
	if action == "resolve" {
		channel = "telegram:resolve"
	}

	return &domain.AckEvent{
		AlertID: alertID,
		UserID:  fmt.Sprintf("%d", update.CallbackQuery.From.ID),
		Channel: channel,
	}, nil
}
