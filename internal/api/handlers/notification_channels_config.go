package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
)

// channelConfigError is a 400 about one field of a channel config. Field is
// the dotted path inside config ("event_types", "auth.oidc.client_id",
// "headers"), so the form can put the message next to the input.
type channelConfigError struct {
	Field string
	Msg   string
}

func (e *channelConfigError) Error() string { return e.Field + ": " + e.Msg }

// writeChannelConfigError answers a typed config problem with a 400 that
// carries the field next to the message. Other errors get a plain 400.
func writeChannelConfigError(w http.ResponseWriter, err error) {
	var ce *channelConfigError
	if errors.As(err, &ce) {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": ce.Error(), "field": ce.Field})
		return
	}
	errorResponse(w, http.StatusBadRequest, err.Error())
}

type cfgKind int

const (
	cfgString cfgKind = iota
	cfgInt
	cfgBool
	cfgStringList
	cfgStringMap
	cfgObject
)

// cfgField describes one key of a channel config. The kinds mirror the Go
// types the provider unmarshals into: a value of the wrong JSON type would
// make every delivery fail at send time, so it is refused at save time.
type cfgField struct {
	kind     cfgKind
	required bool
	example  string              // shown in type errors for lists and maps
	fields   map[string]cfgField // cfgObject only
	// check runs on a well-typed, non-null value and returns a problem.
	check func(v any) string
}

// channelConfigSchemas covers the types whose config the form edits as
// free fields. Email (emailcfg) and SMS/voice (validatePhoneIfNeeded) have
// their own stricter checks and are not listed here.
var channelConfigSchemas = map[string]map[string]cfgField{
	"webhook": {
		"url":     {kind: cfgString, required: true},
		"format":  {kind: cfgString, check: oneOf("generic", "slack", "discord", "teams", "google_chat", "lark", "dingtalk", "synology")},
		"headers": {kind: cfgStringMap, example: `{"X-Api-Key": "..."}`, check: checkWebhookHeaders},
	},
	"slack": {
		"bot_token":  {kind: cfgString, required: true},
		"channel_id": {kind: cfgString, required: true},
	},
	"discord": {
		"bot_token":  {kind: cfgString, required: true},
		"channel_id": {kind: cfgString, required: true},
	},
	"slack_app": {
		"installation_id": {kind: cfgString, required: true},
		"channel_id":      {kind: cfgString, required: true},
		"channel_name":    {kind: cfgString},
	},
	"discord_app": {
		"installation_id": {kind: cfgString, required: true},
		"channel_id":      {kind: cfgString, required: true},
		"channel_name":    {kind: cfgString},
	},
	"telegram": {
		"chat_id":    {kind: cfgString},
		"chat_title": {kind: cfgString},
		"bot_token":  {kind: cfgString},
	},
	"ntfy": {
		"server_url": {kind: cfgString},
		"topic":      {kind: cfgString, required: true},
		"token":      {kind: cfgString},
	},
	"pushover": {
		"api_token": {kind: cfgString, required: true},
		"user_key":  {kind: cfgString, required: true},
		"device":    {kind: cfgString},
		"sound":     {kind: cfgString},
	},
}

// knownChannelType reports whether this build can deliver to channelType.
func knownChannelType(channelType string) bool {
	if channelType == "slack_app" || channelType == "discord_app" {
		return appChannelTypesAvailable(channelType)
	}
	if _, ok := channelConfigSchemas[channelType]; ok {
		return true
	}
	return channelType == emailChannelType || isPhoneChannel(channelType)
}

// validateChannelConfig decodes config against the typed schema of
// channelType and returns a *channelConfigError naming the first bad field
// (in key order, so the answer is stable). existing is the stored config on
// update, or nil on create: a key the stored row already holds is tolerated
// even when this schema does not know it, so legacy rows stay editable, but
// a key new to the row must be one the provider reads.
//
// Types without a schema here (email, SMS, voice) return nil; their own
// validators run separately.
func validateChannelConfig(channelType string, config, existing []byte) error {
	schema, ok := channelConfigSchemas[channelType]
	if !ok {
		return nil
	}
	if len(bytes.TrimSpace(config)) == 0 {
		config = []byte("{}")
	}
	cfg, err := decodeConfigObject(config)
	if err != nil {
		return &channelConfigError{Field: "config", Msg: "must be a JSON object"}
	}
	var prev map[string]any
	if len(existing) > 0 {
		prev, _ = decodeConfigObject(existing) // a broken stored row tolerates nothing
	}
	if err := checkConfigObject("", schema, cfg, prev); err != nil {
		return err
	}
	return checkConfigCrossFields(channelType, cfg)
}

func decodeConfigObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not an object")
	}
	return m, nil
}

func checkConfigObject(prefix string, schema map[string]cfgField, cfg, prev map[string]any) error {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, known := schema[k]; known {
			continue
		}
		if _, legacy := prev[k]; legacy {
			continue
		}
		return &channelConfigError{Field: prefix + k, Msg: "is not a setting this channel type accepts"}
	}

	names := make([]string, 0, len(schema))
	for k := range schema {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		f := schema[k]
		field := prefix + k
		v, present := cfg[k]
		if !present || v == nil {
			if f.required {
				return &channelConfigError{Field: field, Msg: "is required"}
			}
			continue
		}
		if msg := checkConfigKind(f, v); msg != "" {
			return &channelConfigError{Field: field, Msg: msg}
		}
		if f.required && f.kind == cfgString && strings.TrimSpace(v.(string)) == "" {
			return &channelConfigError{Field: field, Msg: "is required"}
		}
		if f.kind == cfgObject {
			prevSub, _ := prev[k].(map[string]any)
			if err := checkConfigObject(field+".", f.fields, v.(map[string]any), prevSub); err != nil {
				return err
			}
		}
		if f.check != nil {
			if msg := f.check(v); msg != "" {
				return &channelConfigError{Field: field, Msg: msg}
			}
		}
	}
	return nil
}

func checkConfigKind(f cfgField, v any) string {
	switch f.kind {
	case cfgString:
		if _, ok := v.(string); !ok {
			return "must be a string"
		}
	case cfgBool:
		if _, ok := v.(bool); !ok {
			return "must be true or false"
		}
	case cfgInt:
		n, ok := v.(json.Number)
		if !ok {
			return "must be a whole number"
		}
		i, err := n.Int64()
		if err != nil || i < 0 || i > math.MaxInt32 {
			return "must be a whole number, 0 or more"
		}
	case cfgStringList:
		list, ok := v.([]any)
		if !ok {
			return "must be a JSON list of strings, like " + f.example
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return "must be a JSON list of strings, like " + f.example
			}
		}
	case cfgStringMap:
		m, ok := v.(map[string]any)
		if !ok {
			return "must be a JSON object of string values, like " + f.example
		}
		for _, item := range m {
			if _, ok := item.(string); !ok {
				return "must be a JSON object of string values, like " + f.example
			}
		}
	case cfgObject:
		if _, ok := v.(map[string]any); !ok {
			return "must be a JSON object"
		}
	}
	return ""
}

func checkConfigCrossFields(channelType string, cfg map[string]any) error {
	if check := channelCrossChecks[channelType]; check != nil {
		return check(cfg)
	}
	return nil
}

func oneOf(allowed ...string) func(any) string {
	return func(v any) string {
		s := v.(string)
		if s == "" {
			return ""
		}
		for _, a := range allowed {
			if s == a {
				return ""
			}
		}
		return "must be one of " + strings.Join(allowed, ", ")
	}
}

const (
	maxWebhookHeaders       = 32
	maxWebhookHeaderValue   = 4096
	maxWebhookHeaderNameLen = 256
)

// webhookManagedHeaders are set by the HTTP client or YipYap itself. A
// custom value would be ignored or would break the request.
var webhookManagedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true,
	"Connection": true, "Content-Type": true,
}

func checkWebhookHeaders(v any) string {
	m := v.(map[string]any)
	if len(m) > maxWebhookHeaders {
		return fmt.Sprintf("at most %d headers are allowed", maxWebhookHeaders)
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if len(k) > maxWebhookHeaderNameLen || !validHeaderName(k) {
			return fmt.Sprintf("%q is not a valid header name", k)
		}
		if webhookManagedHeaders[http.CanonicalHeaderKey(k)] {
			return fmt.Sprintf("%s is set by YipYap and cannot be overridden", http.CanonicalHeaderKey(k))
		}
		val := m[k].(string)
		// The value is a secret in many setups (API keys), so it is never
		// echoed back in the message.
		if len(val) > maxWebhookHeaderValue {
			return fmt.Sprintf("the value for %s is longer than %d characters", k, maxWebhookHeaderValue)
		}
		if !validHeaderValue(val) {
			return fmt.Sprintf("the value for %s contains characters a header cannot hold", k)
		}
	}
	return ""
}

// validHeaderName reports whether s is an RFC 9110 token.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// validHeaderValue refuses control characters (CR and LF above all, which
// would split the header) other than horizontal tab.
func validHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}
