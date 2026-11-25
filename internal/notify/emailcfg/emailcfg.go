// Package emailcfg parses and validates the config of an email ("smtp")
// notification channel. The API handlers and the SMTP provider share it so
// that what is accepted on save is exactly what the notifier sends with.
//
// An email channel runs in one of two modes:
//
//   - platform (default): the channel holds only recipients. Mail goes out
//     through the platform mailer with the platform From address. Server
//     settings in the channel are rejected on save and ignored on send.
//   - smtp: the org brings its own server. Host, port, TLS mode, username,
//     password and From are all required, and nothing is taken from the
//     platform mailer.
//
// Rows saved before the mode field existed are read compatibly: a row with
// a host is smtp mode, any other row is platform mode.
package emailcfg

import (
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"strconv"
	"strings"
)

// Modes.
const (
	ModePlatform = "platform"
	ModeSMTP     = "smtp"
)

// TLS modes for smtp mode. There is no plaintext option: credentials only
// ever travel over TLS.
const (
	TLSStartTLS = "starttls" // plain connect, then a required STARTTLS upgrade
	TLSImplicit = "tls"      // TLS from the first byte (SMTPS, usually port 465)
)

// MaxRecipients caps the number of addresses on one channel.
const MaxRecipients = 10

// Config is an email channel config after mode resolution.
type Config struct {
	Mode     string `json:"mode"`
	To       string `json:"to"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	TLSMode  string `json:"tls_mode,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from,omitempty"`
}

// serverKeys are the config keys that only make sense in smtp mode.
var serverKeys = []string{"host", "port", "tls_mode", "username", "password", "from"}

// FieldError reports a problem with one config field. Field is the JSON key.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return e.Field + " " + e.Msg }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// ErrPlatformUnavailable is returned by Validate for a platform-mode channel
// when this server has no platform mailer configured.
var ErrPlatformUnavailable = &FieldError{
	Field: "mode",
	Msg:   "\"platform\" is not available: this server has no email service configured, so use your own SMTP server",
}

// raw mirrors the stored JSON loosely so legacy and malformed rows can be
// inspected without failing the decode.
type raw map[string]json.RawMessage

func (r raw) str(key string) (string, error) {
	v, ok := r[key]
	if !ok || string(v) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", fieldErr(key, "must be a string")
	}
	return strings.TrimSpace(s), nil
}

// port accepts a JSON number or a numeric string (older UI builds sent
// either).
func (r raw) port() (int, error) {
	v, ok := r["port"]
	if !ok || string(v) == "null" {
		return 0, nil
	}
	var n json.Number
	if err := json.Unmarshal(v, &n); err != nil {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return 0, fieldErr("port", "must be a number")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, nil
		}
		n = json.Number(s)
	}
	i, err := strconv.Atoi(n.String())
	if err != nil {
		return 0, fieldErr("port", "must be a whole number")
	}
	return i, nil
}

// to accepts a single string (comma, semicolon or newline separated) or a
// JSON array of strings.
func (r raw) to() ([]string, error) {
	v, ok := r["to"]
	if !ok || string(v) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(v, &list); err != nil {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fieldErr("to", "must be a string or a list of strings")
		}
		list = strings.FieldsFunc(s, func(c rune) bool {
			return c == ',' || c == ';' || c == '\n' || c == '\r'
		})
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

// set reports whether key holds a non-empty value. Empty strings, zero and
// null count as unset so an older client that posts blank server fields
// with a platform channel is not rejected.
func (r raw) set(key string) bool {
	v, ok := r[key]
	if !ok {
		return false
	}
	switch strings.TrimSpace(string(v)) {
	case "", "null", `""`, "0", `"0"`:
		return false
	}
	return true
}

func parse(data []byte) (raw, error) {
	r := raw{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fieldErr("config", "must be a JSON object")
	}
	return r, nil
}

// resolveMode returns the explicit mode, or infers it for legacy rows.
func resolveMode(r raw) (string, error) {
	m, err := r.str("mode")
	if err != nil {
		return "", err
	}
	switch m {
	case ModePlatform, ModeSMTP:
		return m, nil
	case "":
		if r.set("host") {
			return ModeSMTP, nil
		}
		return ModePlatform, nil
	default:
		return "", fieldErr("mode", "must be %q or %q", ModePlatform, ModeSMTP)
	}
}

// Validate checks an email channel config as submitted on create, update
// or test, and returns it in canonical form. platformAvailable says whether
// this server has a platform mailer; without one, platform mode is refused.
//
// Every returned error is a *FieldError naming the offending key.
func Validate(data []byte, platformAvailable bool) (Config, error) {
	r, err := parse(data)
	if err != nil {
		return Config{}, err
	}
	mode, err := resolveMode(r)
	if err != nil {
		return Config{}, err
	}

	toList, err := r.to()
	if err != nil {
		return Config{}, err
	}
	to, err := normalizeRecipients(toList)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{Mode: mode, To: to}

	if mode == ModePlatform {
		for _, k := range serverKeys {
			if r.set(k) {
				return Config{}, fieldErr(k, "is only allowed when using your own SMTP server (mode %q)", ModeSMTP)
			}
		}
		if !platformAvailable {
			return Config{}, ErrPlatformUnavailable
		}
		return cfg, nil
	}

	// smtp mode: every server field is required.
	if cfg.Host, err = r.str("host"); err != nil {
		return Config{}, err
	}
	if cfg.Host == "" {
		return Config{}, fieldErr("host", "is required")
	}
	if err := validateHost(cfg.Host); err != nil {
		return Config{}, err
	}

	if cfg.Port, err = r.port(); err != nil {
		return Config{}, err
	}
	if cfg.Port == 0 {
		return Config{}, fieldErr("port", "is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, fieldErr("port", "must be between 1 and 65535")
	}

	if cfg.TLSMode, err = r.str("tls_mode"); err != nil {
		return Config{}, err
	}
	switch cfg.TLSMode {
	case TLSStartTLS, TLSImplicit:
	case "":
		return Config{}, fieldErr("tls_mode", "is required")
	default:
		return Config{}, fieldErr("tls_mode", "must be %q or %q", TLSStartTLS, TLSImplicit)
	}

	if cfg.Username, err = r.str("username"); err != nil {
		return Config{}, err
	}
	if cfg.Username == "" {
		return Config{}, fieldErr("username", "is required")
	}

	// The password is not trimmed: leading or trailing spaces may be real.
	if v, ok := r["password"]; ok && string(v) != "null" {
		if err := json.Unmarshal(v, &cfg.Password); err != nil {
			return Config{}, fieldErr("password", "must be a string")
		}
	}
	if cfg.Password == "" {
		return Config{}, fieldErr("password", "is required")
	}

	if cfg.From, err = r.str("from"); err != nil {
		return Config{}, err
	}
	if cfg.From == "" {
		return Config{}, fieldErr("from", "is required")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil || strings.ContainsAny(cfg.From, "\r\n") {
		return Config{}, fieldErr("from", "is not a valid email address")
	}

	return cfg, nil
}

func normalizeRecipients(list []string) (string, error) {
	if len(list) == 0 {
		return "", fieldErr("to", "must list at least one recipient")
	}
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, a := range list {
		addr, err := mail.ParseAddress(a)
		if err != nil || strings.ContainsAny(a, "\r\n") {
			return "", fieldErr("to", "contains an invalid email address: %q", a)
		}
		key := strings.ToLower(addr.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, addr.Address)
	}
	if len(out) > MaxRecipients {
		return "", fieldErr("to", "allows at most %d recipients", MaxRecipients)
	}
	return strings.Join(out, ", "), nil
}

// validateHost accepts a DNS name or an IP literal. Schemes, ports, paths
// and whitespace are rejected; network reachability rules (SSRF) are the
// caller's job.
func validateHost(h string) error {
	if len(h) > 253 {
		return fieldErr("host", "is too long")
	}
	if net.ParseIP(h) != nil {
		return nil
	}
	labels := strings.Split(strings.TrimSuffix(h, "."), ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return fieldErr("host", "must be a hostname such as smtp.example.com")
		}
		for _, c := range l {
			if !hostChar(c) {
				return fieldErr("host", "must be a hostname such as smtp.example.com")
			}
		}
	}
	return nil
}

func hostChar(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		return true
	}
	return false
}

// Marshal returns the canonical JSON for a validated config. Platform-mode
// configs carry only mode and recipients.
func (c Config) Marshal() []byte {
	if c.Mode == ModePlatform {
		c = Config{Mode: ModePlatform, To: c.To}
	}
	b, _ := json.Marshal(c)
	return b
}

// Resolve reads a stored config for sending. It never fails on missing
// fields (legacy rows predate the strict checks); it only resolves the mode
// and drops server settings from platform-mode configs so the provider
// cannot use them.
func Resolve(data []byte) (Config, error) {
	r, err := parse(data)
	if err != nil {
		return Config{}, err
	}
	mode, err := resolveMode(r)
	if err != nil {
		return Config{}, err
	}
	toList, err := r.to()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Mode: mode, To: strings.Join(toList, ", ")}
	if mode == ModePlatform {
		return cfg, nil
	}
	if cfg.Host, err = r.str("host"); err != nil {
		return Config{}, err
	}
	if cfg.Port, err = r.port(); err != nil {
		return Config{}, err
	}
	if cfg.TLSMode, err = r.str("tls_mode"); err != nil {
		return Config{}, err
	}
	if cfg.Username, err = r.str("username"); err != nil {
		return Config{}, err
	}
	if v, ok := r["password"]; ok && string(v) != "null" {
		if err := json.Unmarshal(v, &cfg.Password); err != nil {
			return Config{}, fieldErr("password", "must be a string")
		}
	}
	if cfg.From, err = r.str("from"); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Recipients splits the To field into bare addresses. Entries that do not
// parse are dropped.
func (c Config) Recipients() []string {
	var out []string
	for _, a := range strings.FieldsFunc(c.To, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		if addr, err := mail.ParseAddress(strings.TrimSpace(a)); err == nil {
			out = append(out, addr.Address)
		}
	}
	return out
}
