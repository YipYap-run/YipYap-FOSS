package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// monitorTargetError is a refused monitor target. Field is the request
// path of the offending input (config.url, config.host, ...) so the monitor
// form can mark that input; Msg is the text shown next to it.
type monitorTargetError struct {
	Field string
	Msg   string
	cause error
}

func (e *monitorTargetError) Error() string { return e.Field + ": " + e.Msg }
func (e *monitorTargetError) Unwrap() error { return e.cause }

// writeMonitorTargetError responds 400 with the field error shape used by
// the other forms ({"error", "field", "fields"}).
func writeMonitorTargetError(w http.ResponseWriter, err error) {
	var te *monitorTargetError
	if errors.As(err, &te) {
		writeFieldErrors(w, []string{te.Field}, fieldErrors{te.Field: te.Msg})
		return
	}
	errorResponse(w, http.StatusBadRequest, err.Error())
}

// targetError turns a checker validation error into text a person can act
// on. The checker wording stays as is for logs and other callers.
func targetError(field, host string, err error) error {
	if err == nil {
		return nil
	}
	var msg string
	switch {
	case errors.Is(err, checker.ErrUnresolvable):
		msg = host + " does not resolve. Check the spelling, or wait until its DNS record is published."
	case checker.IsPrivateTargetError(err):
		// The checker adds "(name resolves to 10.0.0.5)" when a name led
		// to the private address; keep that, it explains split DNS.
		detail := ""
		if i := strings.Index(err.Error(), " ("); i >= 0 {
			detail = " " + strings.TrimSuffix(err.Error()[i+1:], ")") + ")"
		}
		msg = "Monitors can only check hosts on the public internet. This target is a private or internal address" + detail + "."
	default:
		msg = sentence(err.Error())
	}
	return &monitorTargetError{Field: field, Msg: msg, cause: err}
}

// sentence capitalizes s and ends it with a period.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// validateMonitorTarget performs SSRF validation for the monitor's target
// host(s) based on the monitor type. A refusal is a *monitorTargetError
// naming the config field it came from.
func validateMonitorTarget(monitorType domain.MonitorType, config json.RawMessage) error {
	switch monitorType {
	case domain.MonitorHTTP:
		var cfg struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(config, &cfg) == nil && cfg.URL != "" {
			host := cfg.URL
			if u, err := url.Parse(cfg.URL); err == nil && u.Hostname() != "" {
				host = u.Hostname()
			}
			return targetError("config.url", host, checker.ValidateHTTPTarget(cfg.URL))
		}
	case domain.MonitorTCP, domain.MonitorPing:
		var cfg struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(config, &cfg) == nil && cfg.Host != "" {
			return targetError("config.host", cfg.Host, checker.ValidateTarget(cfg.Host))
		}
	case domain.MonitorDNS:
		var cfg struct {
			Hostname   string `json:"hostname"`
			Nameserver string `json:"nameserver,omitempty"`
		}
		if json.Unmarshal(config, &cfg) == nil {
			if cfg.Nameserver != "" {
				if err := targetError("config.nameserver", cfg.Nameserver, checker.ValidateTarget(cfg.Nameserver)); err != nil {
					return err
				}
			}
			if cfg.Hostname != "" {
				// The query name is looked up, not dialed, so a name with
				// only MX or TXT records is fine. The nameserver above is
				// dialed and must resolve.
				return targetError("config.hostname", cfg.Hostname, checker.ValidateDNSQueryName(cfg.Hostname))
			}
		}
	}
	return nil
}
