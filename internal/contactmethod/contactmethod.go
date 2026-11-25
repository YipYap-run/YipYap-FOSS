// Package contactmethod validates per-user contact methods and turns a
// verified method into a NotificationJob for the existing notify providers.
// It is shared by the settings API and the escalation engine so both build
// the same delivery target from the same stored config.
package contactmethod

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Limits on how many methods and rules one user may keep.
const (
	MaxMethodsPerUser = 20
	MaxRulesPerUser   = 50
	MaxDelayMinutes   = 1440
	MaxLabelLength    = 100
)

// Sentinel is echoed in place of secret config fields on read. Sending it
// back on update keeps the stored value.
const Sentinel = "••••••••"

// ValidationError is a user-facing input error.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// PlanRequiredError means the org's plan does not include a contact method
// type. Msg is user-facing.
type PlanRequiredError struct{ Msg string }

func (e *PlanRequiredError) Error() string { return e.Msg }

// IsValidation reports whether err is a user-facing input error.
func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

// Per-type stored config shapes.
type emailConfig struct {
	Address string `json:"address"`
}

type phoneConfig struct {
	Phone string `json:"phone"`
}

type ntfyConfig struct {
	ServerURL string `json:"server_url,omitempty"`
	Topic     string `json:"topic"`
	Token     string `json:"token,omitempty"`
}

type pushoverConfig struct {
	UserKey  string `json:"user_key"`
	APIToken string `json:"api_token"`
	Device   string `json:"device,omitempty"`
}

type pagerDutyConfig struct {
	RoutingKey string `json:"routing_key"`
}

type webhookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

type slackDMConfig struct {
	SlackUserID  string `json:"slack_user_id"`
	ViaChannelID string `json:"via_channel_id"`
}

type discordDMConfig struct {
	DMChannelID  string `json:"dm_channel_id"`
	ViaChannelID string `json:"via_channel_id"`
}

type telegramConfig struct {
	ChatID       string `json:"chat_id"`
	ViaChannelID string `json:"via_channel_id,omitempty"`
}

// secretFields lists config fields that are write-only.
var secretFields = map[domain.ContactMethodType][]string{
	domain.ContactNtfy:      {"token"},
	domain.ContactPushover:  {"api_token"},
	domain.ContactPagerDuty: {"routing_key"},
	domain.ContactWebhook:   {"secret"},
}

var (
	e164Re        = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	ntfyTopicRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	pushoverKeyRe = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	pushoverDevRe = regexp.MustCompile(`^[A-Za-z0-9_-]{0,25}$`)
	routingKeyRe  = regexp.MustCompile(`^[A-Za-z0-9]{20,64}$`)
	slackUserRe   = regexp.MustCompile(`^[UW][A-Z0-9]{2,20}$`)
	snowflakeRe   = regexp.MustCompile(`^[0-9]{15,21}$`)
	telegramIDRe  = regexp.MustCompile(`^-?[0-9]{1,20}$`)
)

// KnownType reports whether t is a contact method type this server knows.
func KnownType(t domain.ContactMethodType) bool {
	for _, k := range domain.AllContactMethodTypes {
		if k == t {
			return true
		}
	}
	return false
}

// NormalizePhone strips common separators and checks the result is an
// E.164 number (+, country code, up to 15 digits).
func NormalizePhone(raw string) (string, error) {
	p := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')', '.':
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	if !e164Re.MatchString(p) {
		return "", invalid("phone number must be in international format, for example +15551234567")
	}
	if err := phoneDeliverable(p); err != nil {
		return "", invalid("%s", err.Error())
	}
	return p, nil
}

// NormalizeEmail checks raw is a bare email address and returns it trimmed.
func NormalizeEmail(raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" || len(addr) > 254 || strings.ContainsAny(addr, "\r\n") {
		return "", invalid("enter a valid email address")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed.Address != addr || parsed.Name != "" {
		return "", invalid("enter a valid email address")
	}
	at := strings.LastIndex(addr, "@")
	if at < 1 || !strings.Contains(addr[at+1:], ".") {
		return "", invalid("enter a valid email address")
	}
	return addr, nil
}

func decodeStrict(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return invalid("config is required")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("config is not valid for this contact method type")
	}
	return nil
}

// mergeSecrets replaces sentinel values in raw with the stored values from
// existing, so a client can echo a redacted config back unchanged.
func mergeSecrets(t domain.ContactMethodType, raw json.RawMessage, existing string) json.RawMessage {
	fields := secretFields[t]
	if len(fields) == 0 || existing == "" {
		return raw
	}
	var in, old map[string]any
	if json.Unmarshal(raw, &in) != nil || json.Unmarshal([]byte(existing), &old) != nil {
		return raw
	}
	for _, f := range fields {
		if v, ok := in[f].(string); ok && v == Sentinel {
			if ov, ok := old[f]; ok {
				in[f] = ov
			} else {
				delete(in, f)
			}
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		return raw
	}
	return out
}

// Normalize validates raw for type t in org orgID and returns the canonical
// config JSON to store. existing is the stored config on update ("" on
// create); secret fields sent back as Sentinel keep their stored value.
func Normalize(ctx context.Context, s store.Store, orgID string, t domain.ContactMethodType, raw json.RawMessage, existing string) (string, error) {
	if !KnownType(t) {
		return "", invalid("unknown contact method type %q", t)
	}
	if !TypeSupported(t) {
		return "", invalid("%s contact methods are not available on this server", t)
	}
	raw = mergeSecrets(t, raw, existing)

	var out any
	switch t {
	case domain.ContactEmail:
		var c emailConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		addr, err := NormalizeEmail(c.Address)
		if err != nil {
			return "", err
		}
		out = emailConfig{Address: addr}
	case domain.ContactSMS, domain.ContactVoice:
		var c phoneConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		p, err := NormalizePhone(c.Phone)
		if err != nil {
			return "", err
		}
		out = phoneConfig{Phone: p}
	case domain.ContactNtfy:
		var c ntfyConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.Topic = strings.TrimSpace(c.Topic)
		if !ntfyTopicRe.MatchString(c.Topic) {
			return "", invalid("ntfy topic must be 1 to 64 letters, digits, dashes or underscores")
		}
		c.ServerURL = strings.TrimRight(strings.TrimSpace(c.ServerURL), "/")
		if c.ServerURL != "" {
			u, err := url.Parse(c.ServerURL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return "", invalid("ntfy server must be an https:// URL")
			}
			if err := checker.ValidateHTTPTarget(c.ServerURL); err != nil {
				return "", invalid("ntfy server is not allowed: %s", err.Error())
			}
		}
		if len(c.Token) > 256 || strings.ContainsAny(c.Token, "\r\n") {
			return "", invalid("ntfy access token is not valid")
		}
		out = c
	case domain.ContactPushover:
		var c pushoverConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.UserKey, c.APIToken, c.Device = strings.TrimSpace(c.UserKey), strings.TrimSpace(c.APIToken), strings.TrimSpace(c.Device)
		if !pushoverKeyRe.MatchString(c.UserKey) {
			return "", invalid("Pushover user key must be 30 letters or digits")
		}
		if !pushoverKeyRe.MatchString(c.APIToken) {
			return "", invalid("Pushover application token must be 30 letters or digits")
		}
		if !pushoverDevRe.MatchString(c.Device) {
			return "", invalid("Pushover device name may only use letters, digits, dashes or underscores (25 max)")
		}
		out = c
	case domain.ContactPagerDuty:
		var c pagerDutyConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.RoutingKey = strings.TrimSpace(c.RoutingKey)
		if !routingKeyRe.MatchString(c.RoutingKey) {
			return "", invalid("PagerDuty routing key must be 20 to 64 letters or digits")
		}
		out = c
	case domain.ContactWebhook:
		var c webhookConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.URL, c.Secret = strings.TrimSpace(c.URL), strings.TrimSpace(c.Secret)
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return "", invalid("webhook URL must be an http:// or https:// URL")
		}
		if err := checker.ValidateControlPlaneHTTPTarget(c.URL); err != nil {
			return "", invalid("webhook URL is not allowed: %s", err.Error())
		}
		if c.Secret != "" && (len(c.Secret) < 16 || len(c.Secret) > 256 || strings.ContainsAny(c.Secret, "\r\n ")) {
			return "", invalid("webhook signing secret must be 16 to 256 characters with no spaces")
		}
		out = c
	case domain.ContactSlackDM:
		var c slackDMConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.SlackUserID = strings.TrimSpace(c.SlackUserID)
		if !slackUserRe.MatchString(c.SlackUserID) {
			return "", invalid("Slack member ID looks like U0123ABCD (Profile, then Copy member ID)")
		}
		if err := checkViaChannel(ctx, s, orgID, c.ViaChannelID, "slack", true); err != nil {
			return "", err
		}
		out = c
	case domain.ContactDiscordDM:
		var c discordDMConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.DMChannelID = strings.TrimSpace(c.DMChannelID)
		if !snowflakeRe.MatchString(c.DMChannelID) {
			return "", invalid("Discord DM channel ID must be the numeric ID of your direct message with the bot")
		}
		if err := checkViaChannel(ctx, s, orgID, c.ViaChannelID, "discord", true); err != nil {
			return "", err
		}
		out = c
	case domain.ContactTelegram:
		var c telegramConfig
		if err := decodeStrict(raw, &c); err != nil {
			return "", err
		}
		c.ChatID = strings.TrimSpace(c.ChatID)
		if !telegramIDRe.MatchString(c.ChatID) {
			return "", invalid("Telegram chat ID must be a number")
		}
		if err := checkViaChannel(ctx, s, orgID, c.ViaChannelID, "telegram", false); err != nil {
			return "", err
		}
		out = c
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkViaChannel confirms channelID names an org channel of chType. The
// personal DM borrows that channel's bot credentials for delivery.
func checkViaChannel(ctx context.Context, s store.Store, orgID, channelID, chType string, required bool) error {
	if channelID == "" {
		if required {
			return invalid("choose the %s integration to send through", chType)
		}
		return nil
	}
	ch, err := s.NotificationChannels().GetByID(ctx, channelID)
	if err != nil || ch == nil || ch.OrgID != orgID || ch.Type != chType {
		return invalid("the selected %s integration was not found", chType)
	}
	return nil
}

// Target returns the provider channel and the plaintext provider config
// (the same JSON shape a shared channel stores) for a contact method.
func Target(ctx context.Context, s store.Store, m *domain.ContactMethod) (string, []byte, error) {
	var (
		channel string
		cfg     any
	)
	switch m.Type {
	case domain.ContactEmail:
		var c emailConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		channel, cfg = "smtp", map[string]string{"to": c.Address}
	case domain.ContactSMS, domain.ContactVoice:
		var c phoneConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		ch, ok := phoneChannel(m.Type)
		if !ok {
			return "", nil, fmt.Errorf("%s delivery is not available on this server", m.Type)
		}
		channel, cfg = ch, map[string]string{"to": c.Phone}
	case domain.ContactNtfy:
		var c ntfyConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		channel, cfg = "ntfy", c
	case domain.ContactPushover:
		var c pushoverConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		channel, cfg = "pushover", c
	case domain.ContactPagerDuty:
		var c pagerDutyConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		channel, cfg = "pagerduty", c
	case domain.ContactWebhook:
		var c webhookConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		channel, cfg = "webhook", map[string]string{"url": c.URL, "secret": c.Secret, "format": "generic"}
	case domain.ContactSlackDM:
		var c slackDMConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		tok, err := viaBotToken(ctx, s, m.OrgID, c.ViaChannelID, "slack")
		if err != nil {
			return "", nil, err
		}
		// chat.postMessage with a member ID posts to the bot's DM with them.
		channel, cfg = "slack", map[string]string{"bot_token": tok, "channel_id": c.SlackUserID}
	case domain.ContactDiscordDM:
		var c discordDMConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		tok, err := viaBotToken(ctx, s, m.OrgID, c.ViaChannelID, "discord")
		if err != nil {
			return "", nil, err
		}
		channel, cfg = "discord", map[string]string{"bot_token": tok, "channel_id": c.DMChannelID}
	case domain.ContactTelegram:
		var c telegramConfig
		if err := json.Unmarshal([]byte(m.Config), &c); err != nil {
			return "", nil, err
		}
		tc := map[string]string{"chat_id": c.ChatID}
		if c.ViaChannelID != "" {
			tok, err := viaBotToken(ctx, s, m.OrgID, c.ViaChannelID, "telegram")
			if err != nil {
				return "", nil, err
			}
			if tok != "" {
				tc["bot_token"] = tok
			}
		}
		// With no bot token the provider falls back to the platform bot.
		channel, cfg = "telegram", tc
	default:
		return "", nil, fmt.Errorf("unknown contact method type %q", m.Type)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	return channel, b, nil
}

func viaBotToken(ctx context.Context, s store.Store, orgID, channelID, chType string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("no %s integration selected", chType)
	}
	ch, err := s.NotificationChannels().GetByID(ctx, channelID)
	if err != nil || ch == nil || ch.OrgID != orgID || ch.Type != chType {
		return "", fmt.Errorf("%s integration not found", chType)
	}
	if !ch.Enabled {
		return "", fmt.Errorf("%s integration %q is disabled", chType, ch.Name)
	}
	var c struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.Unmarshal([]byte(ch.Config), &c); err != nil {
		return "", fmt.Errorf("%s integration config is invalid", chType)
	}
	if c.BotToken == "" && chType != "telegram" {
		return "", fmt.Errorf("%s integration has no bot token", chType)
	}
	return c.BotToken, nil
}

// Encrypter seals provider config for transit (crypto.Envelope.Encrypt).
type Encrypter func(plaintext []byte) ([]byte, error)

type Decrypter func(ciphertext []byte) ([]byte, error)

type Cipher struct {
	Enc Encrypter
	Dec Decrypter
}

const sealedPrefix = "sealed:v1:"

func SealConfig(t domain.ContactMethodType, cfg string, enc Encrypter) (string, error) {
	fields := secretFields[t]
	if len(fields) == 0 || cfg == "" || enc == nil {
		return cfg, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		return "", err
	}
	changed := false
	for _, f := range fields {
		v, ok := m[f].(string)
		if !ok || v == "" || strings.HasPrefix(v, sealedPrefix) {
			continue
		}
		ct, err := enc([]byte(v))
		if err != nil {
			return "", err
		}
		m[f] = sealedPrefix + base64.StdEncoding.EncodeToString(ct)
		changed = true
	}
	if !changed {
		return cfg, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func OpenConfig(t domain.ContactMethodType, cfg string, dec Decrypter) (string, error) {
	fields := secretFields[t]
	if len(fields) == 0 || cfg == "" {
		return cfg, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		return "", err
	}
	changed := false
	for _, f := range fields {
		v, ok := m[f].(string)
		if !ok || !strings.HasPrefix(v, sealedPrefix) {
			continue
		}
		if dec == nil {
			return "", fmt.Errorf("contact method config is encrypted but no decryption key is configured")
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, sealedPrefix))
		if err != nil {
			return "", err
		}
		pt, err := dec(raw)
		if err != nil {
			return "", err
		}
		m[f] = string(pt)
		changed = true
	}
	if !changed {
		return cfg, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// EncodeTargetConfig wraps provider config in the versioned envelope the
// providers expect: 0x01 + ciphertext when enc is set, else 0x00 + JSON,
// then base64.
func EncodeTargetConfig(cfg []byte, enc Encrypter) (string, error) {
	var versioned []byte
	if enc != nil {
		ct, err := enc(cfg)
		if err != nil {
			return "", err
		}
		versioned = append([]byte{0x01}, ct...)
	} else {
		versioned = append([]byte{0x00}, cfg...)
	}
	return base64.StdEncoding.EncodeToString(versioned), nil
}

// FillJob points job at contact method m: it sets Channel, TargetConfig,
// TargetUserID and ContactMethodID. Everything else on job is left as the
// caller set it.
func FillJob(ctx context.Context, s store.Store, m *domain.ContactMethod, job *domain.NotificationJob, cipher Cipher) error {
	plain, err := OpenConfig(m.Type, m.Config, cipher.Dec)
	if err != nil {
		return err
	}
	target := *m
	target.Config = plain
	channel, cfg, err := Target(ctx, s, &target)
	if err != nil {
		return err
	}
	tc, err := EncodeTargetConfig(cfg, cipher.Enc)
	if err != nil {
		return err
	}
	job.Channel = channel
	job.TargetConfig = tc
	job.TargetUserID = m.UserID
	job.ContactMethodID = m.ID
	return nil
}

// Redacted returns the config for the owner, with secret fields replaced by
// Sentinel.
func Redacted(m *domain.ContactMethod) json.RawMessage {
	fields := secretFields[m.Type]
	if len(fields) == 0 {
		return json.RawMessage(m.Config)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(m.Config), &cfg); err != nil {
		return json.RawMessage(`{}`)
	}
	for _, f := range fields {
		if v, ok := cfg[f].(string); ok && v != "" {
			cfg[f] = Sentinel
		}
	}
	b, _ := json.Marshal(cfg)
	return b
}

// Summary returns a short, partly masked description of where the method
// delivers, for admins viewing a member's methods.
func Summary(m *domain.ContactMethod) string {
	var c map[string]string
	_ = json.Unmarshal([]byte(m.Config), &c)
	switch m.Type {
	case domain.ContactEmail:
		addr := c["address"]
		at := strings.LastIndex(addr, "@")
		if at < 1 {
			return ""
		}
		return addr[:1] + "•••" + addr[at:]
	case domain.ContactSMS, domain.ContactVoice:
		p := c["phone"]
		if len(p) < 8 {
			return ""
		}
		return p[:2] + strings.Repeat("•", len(p)-6) + p[len(p)-4:]
	case domain.ContactNtfy:
		return "ntfy topic"
	case domain.ContactPushover:
		return "Pushover"
	case domain.ContactPagerDuty:
		return "PagerDuty"
	case domain.ContactWebhook:
		u, err := url.Parse(c["url"])
		if err != nil || u.Host == "" {
			return "Webhook"
		}
		return "Webhook " + u.Host
	case domain.ContactSlackDM:
		return "Slack direct message"
	case domain.ContactDiscordDM:
		return "Discord direct message"
	case domain.ContactTelegram:
		return "Telegram chat"
	}
	return ""
}

// NewCode returns a random 6-digit code and its hash bound to methodID.
func NewCode(methodID string) (code, hash string, err error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", "", err
	}
	code = fmt.Sprintf("%06d", n.Int64())
	return code, HashCode(methodID, code), nil
}

// HashCode hashes a verification code together with the method ID.
func HashCode(methodID, code string) string {
	sum := sha256.Sum256([]byte(methodID + ":" + strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

// CodeMatches compares code against the stored hash in constant time.
func CodeMatches(methodID, code, storedHash string) bool {
	if storedHash == "" {
		return false
	}
	got := HashCode(methodID, code)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}
