package handlers

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// Write-only channel secrets.
//
// Secret config values are replaced with configSentinel on every read, for
// every role. On update the client echoes the sentinel to keep the stored
// value, sends a new value to replace it, or leaves the key out to delete
// it. A sentinel with no stored value behind it is refused rather than
// saved as a literal.
//
// Two kinds of secret exist:
//   - fixed paths listed in secretFields (tokens, passwords, header values);
//   - endpoint URLs that are themselves the credential (incoming-webhook URLs
//     for Slack, Discord, Teams and similar, or any URL with a token in its
//     path, query or userinfo). Those are hidden whole and the response
//     carries only scheme and host in url_host, with url_set=true.

// writeOnlyURLFields names the endpoint URL key per channel type that may
// carry a credential.
var writeOnlyURLFields = map[string]string{
	"webhook": "url",
}

// incomingWebhookFormats are webhook formats whose URL is always an
// incoming-webhook URL with the posting credential in it.
var incomingWebhookFormats = map[string]bool{
	"slack": true, "discord": true, "teams": true, "google_chat": true,
	"lark": true, "dingtalk": true, "synology": true,
}

// incomingWebhookHosts are hosts that only serve credential-bearing
// incoming-webhook URLs. A leading "." matches any subdomain.
var incomingWebhookHosts = []string{
	"hooks.slack.com",
	"discord.com", "discordapp.com", ".discord.com", ".discordapp.com",
	".webhook.office.com", "outlook.office.com", "outlook.office365.com",
	".logic.azure.com",
	"chat.googleapis.com",
	"oapi.dingtalk.com",
	"open.feishu.cn", "open.larksuite.com",
	"hooks.zapier.com", "maker.ifttt.com",
}

// urlRedaction is what a response says about a hidden endpoint URL.
type urlRedaction struct {
	Field string // config key, e.g. "url"
	Host  string // scheme://host[:port], "" when the URL did not parse
}

// isWriteOnlyURL reports whether raw must never be returned. format is the
// webhook format ("" for other types).
func isWriteOnlyURL(raw, format string) bool {
	if raw == "" {
		return false
	}
	if incomingWebhookFormats[format] {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Cannot show it safely in part, so hide it.
		return true
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range incomingWebhookHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if tokenLike(seg) {
			return true
		}
	}
	return false
}

// tokenLike reports whether a path segment looks like a generated secret:
// long and made of token characters, mixing letters and digits, or very
// long regardless. Names like "alerts" or "yipyap-hooks" stay visible.
func tokenLike(seg string) bool {
	if len(seg) < 16 {
		return false
	}
	letters, digits := false, false
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			letters = true
		case c >= '0' && c <= '9':
			digits = true
		case c == '-' || c == '_' || c == '.' || c == '~' || c == '%' || c == '=' || c == '+':
		default:
			return false
		}
	}
	return (letters && digits) || len(seg) >= 32
}

// urlHost returns scheme://host for display, or "" when raw does not parse.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// redactPath replaces the value at a dotted JSON path with configSentinel.
// Missing intermediate nodes are a no-op (so a partially-populated config
// is tolerated rather than having keys synthesised). A final "*" redacts
// every value of the map it names.
func redactPath(m map[string]interface{}, path string) {
	parts := strings.Split(path, ".")
	for i, p := range parts {
		if i == len(parts)-1 {
			if p == "*" {
				for k := range m {
					m[k] = configSentinel
				}
				return
			}
			if _, ok := m[p]; ok {
				m[p] = configSentinel
			}
			return
		}
		nested, ok := m[p].(map[string]interface{})
		if !ok {
			return
		}
		m = nested
	}
}

// mergePath preserves the value at path in newM whenever it is the sentinel
// and the existing map has a value there. For non-sentinel values the edit
// is kept as-is, which is what lets callers actually rotate a secret. With
// a final "*", each map entry is merged on its own: a sentinel keeps the
// stored value for that key (matched exactly, then case-insensitively,
// since header names are), a new value replaces it, and a key left out of
// newM is gone.
func mergePath(newM, existM map[string]interface{}, path string) {
	parts := strings.Split(path, ".")
	// Walk parallel maps, bailing if the path doesn't exist on either side.
	for i, p := range parts {
		if i == len(parts)-1 {
			if p == "*" {
				for k, nv := range newM {
					if s, ok := nv.(string); !ok || s != configSentinel {
						continue
					}
					if ev, ok := lookupFold(existM, k); ok {
						newM[k] = ev
					}
				}
				return
			}
			nv, ok := newM[p]
			if !ok {
				return
			}
			s, ok := nv.(string)
			if !ok || s != configSentinel {
				return
			}
			if ev, ok := existM[p]; ok {
				newM[p] = ev
			}
			return
		}
		nn, ok := newM[p].(map[string]interface{})
		if !ok {
			return
		}
		en, ok := existM[p].(map[string]interface{})
		if !ok {
			return
		}
		newM = nn
		existM = en
	}
}

func lookupFold(m map[string]interface{}, key string) (interface{}, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

// redactConfig hides every secret in config and reports a hidden endpoint
// URL, if any.
func redactConfig(channelType, config string) (string, *urlRedaction) {
	fields := secretFields[channelType]
	urlField := writeOnlyURLFields[channelType]
	if (len(fields) == 0 && urlField == "") || config == "" {
		return config, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(config), &m); err != nil {
		// An unreadable row could hold anything, so none of it is returned.
		return "{}", nil
	}
	for _, f := range fields {
		redactPath(m, f)
	}
	var red *urlRedaction
	if urlField != "" {
		raw, _ := m[urlField].(string)
		format, _ := m["format"].(string)
		if isWriteOnlyURL(raw, format) {
			m[urlField] = configSentinel
			red = &urlRedaction{Field: urlField, Host: urlHost(raw)}
		}
	}
	out, _ := json.Marshal(m)
	return string(out), red
}

// mergeConfig puts stored secrets back wherever newConfig echoes the
// sentinel.
func mergeConfig(channelType, newConfig, existingConfig string) string {
	fields := secretFields[channelType]
	urlField := writeOnlyURLFields[channelType]
	if len(fields) == 0 && urlField == "" {
		return newConfig
	}
	var newM, existM map[string]interface{}
	if err := json.Unmarshal([]byte(newConfig), &newM); err != nil {
		return newConfig
	}
	if err := json.Unmarshal([]byte(existingConfig), &existM); err != nil {
		return newConfig
	}
	for _, f := range fields {
		mergePath(newM, existM, f)
	}
	if urlField != "" {
		mergePath(newM, existM, urlField)
	}
	out, _ := json.Marshal(newM)
	return string(out)
}

// unresolvedSentinel returns the config path of the first secret that
// still holds the sentinel after merging, or "" when there is none. That
// happens when a client echoes a sentinel for a value that was never
// stored (a new header, a create call); saving it would send the bullet
// characters as the credential.
func unresolvedSentinel(channelType string, config []byte) string {
	fields := secretFields[channelType]
	urlField := writeOnlyURLFields[channelType]
	if (len(fields) == 0 && urlField == "") || len(config) == 0 {
		return ""
	}
	var m map[string]interface{}
	if json.Unmarshal(config, &m) != nil {
		return ""
	}
	paths := append([]string(nil), fields...)
	if urlField != "" {
		paths = append(paths, urlField)
	}
	for _, p := range paths {
		if hit := sentinelAt(m, p); hit != "" {
			return hit
		}
	}
	return ""
}

func sentinelAt(m map[string]interface{}, path string) string {
	parts := strings.Split(path, ".")
	prefix := ""
	for i, p := range parts {
		if i == len(parts)-1 {
			if p == "*" {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if s, ok := m[k].(string); ok && s == configSentinel {
						return prefix + k
					}
				}
				return ""
			}
			if s, ok := m[p].(string); ok && s == configSentinel {
				return prefix + p
			}
			return ""
		}
		nested, ok := m[p].(map[string]interface{})
		if !ok {
			return ""
		}
		m = nested
		prefix += p + "."
	}
	return ""
}

// unresolvedSentinelError is the 400 for unresolvedSentinel.
func unresolvedSentinelError(field string) error {
	return &channelConfigError{Field: field, Msg: "has no saved value to keep; enter a value"}
}
