package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// StatusError reports that a destination answered with a non-2xx HTTP
// status. Detail holds a short excerpt of the response body for server
// logs; it must never be shown to the org (see PublicDeliveryError).
type StatusError struct {
	Provider   string
	StatusCode int
	Detail     string
}

func (e *StatusError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s returned status %d", e.Provider, e.StatusCode)
	}
	return fmt.Sprintf("%s returned status %d: %s", e.Provider, e.StatusCode, e.Detail)
}

// RecipientError is a provider error whose Msg is written for the person
// who owns the destination, such as "Discord will not deliver to this
// user". Msg must never carry upstream bodies, addresses or credentials.
// Err keeps the underlying error for logs and errors.Is.
type RecipientError struct {
	Msg string
	Err error
}

func (e *RecipientError) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *RecipientError) Unwrap() error { return e.Err }

// PublicDeliveryError turns a provider error into a message that is safe
// to show to org members. It names the class of failure only: no upstream
// response body, resolved address, or internal error text, so a channel
// pointed at an internal service cannot be used to read or map it.
func PublicDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	var re *RecipientError
	if errors.As(err, &re) && re.Msg != "" {
		return "Delivery failed: " + re.Msg
	}
	var se *StatusError
	switch {
	case errors.Is(err, checker.ErrSSRFBlocked):
		return "Delivery failed: the destination address is not allowed."
	case errors.As(err, &se):
		switch {
		case se.StatusCode >= 300 && se.StatusCode < 400:
			return "Delivery failed: the destination answered with a redirect, and redirects are not followed."
		case se.StatusCode >= 500:
			return "Delivery failed: the destination returned HTTP 5xx."
		default:
			return fmt.Sprintf("Delivery failed: the destination returned HTTP %d.", se.StatusCode)
		}
	case errors.Is(err, context.DeadlineExceeded):
		return "Delivery failed: the destination did not respond in time."
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		switch {
		case tpErr.Code == 530 || tpErr.Code == 534 || tpErr.Code == 535:
			return "Delivery failed: the mail server rejected the login."
		case tpErr.Code >= 550 && tpErr.Code <= 553:
			return "Delivery failed: the mail server rejected a recipient or sender address."
		default:
			return fmt.Sprintf("Delivery failed: the mail server returned code %d.", tpErr.Code)
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return "Delivery failed: the destination host name does not resolve."
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "Delivery failed: the destination did not respond in time."
		}
		return "Delivery failed: could not connect to the destination."
	}
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		return "Delivery failed: could not connect to the destination."
	}
	return "Delivery failed. Check the channel settings and try again."
}

// LogSafeError returns err's text for server logs with any request URL cut
// down to scheme and host. Provider URLs can carry credentials (a Telegram
// bot token in the path, a Slack or Discord incoming webhook secret), and
// Go's HTTP client puts the full URL in transport errors.
func LogSafeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		redacted := "[redacted url]"
		if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" {
			redacted = u.Scheme + "://" + u.Host + "/[redacted]"
		}
		msg = strings.ReplaceAll(msg, ue.URL, redacted)
	}
	return msg
}
