package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

func TestPublicDeliveryError(t *testing.T) {
	connRefused := &url.Error{Op: "Post", URL: "http://10.0.0.7:8080/x", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"ssrf", &url.Error{Op: "Post", URL: "http://169.254.169.254/", Err: fmt.Errorf("%w: 169.254.169.254 (link-local)", checker.ErrSSRFBlocked)}, "not allowed"},
		{"5xx", fmt.Errorf("wrap: %w", &StatusError{Provider: "webhook", StatusCode: 502, Detail: "nginx at 10.0.0.7"}), "HTTP 5xx"},
		{"4xx", &StatusError{Provider: "webhook", StatusCode: 404}, "HTTP 404"},
		{"redirect", &StatusError{Provider: "webhook", StatusCode: 302}, "redirect"},
		{"refused", connRefused, "could not connect"},
		{"deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), "in time"},
		{"other", errors.New("smtp 535 auth failed for user@10.0.0.7"), "Check the channel settings"},
		{"smtp auth", fmt.Errorf("smtp send: %w", &textproto.Error{Code: 535, Msg: "5.7.8 user@10.0.0.7 bad credentials"}), "rejected the login"},
		{"smtp rcpt", &textproto.Error{Code: 550, Msg: "no such user user@10.0.0.7"}, "recipient or sender"},
		{"smtp other", &textproto.Error{Code: 421, Msg: "nginx busy at 10.0.0.7"}, "code 421"},
		{"dns", &url.Error{Op: "Post", URL: "https://nope.invalid/x", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "10.0.0.7.invalid", IsNotFound: true}}}, "does not resolve"},
	}
	for _, tc := range cases {
		got := PublicDeliveryError(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, got, tc.want)
		}
		for _, leak := range []string{"10.0.0.7", "169.254", "nginx", "user@"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: %q leaks %q", tc.name, got, leak)
			}
		}
	}
	if PublicDeliveryError(nil) != "" {
		t.Error("nil error should map to empty string")
	}
}

func TestLogSafeError(t *testing.T) {
	token := "123456:AAE-secret"
	err := fmt.Errorf("telegram POST: %w", &url.Error{
		Op: "Post", URL: "https://api.telegram.org/bot" + token + "/sendMessage",
		Err: errors.New("connection refused"),
	})
	got := LogSafeError(err)
	if strings.Contains(got, token) {
		t.Fatalf("token leaked: %q", got)
	}
	if !strings.Contains(got, "api.telegram.org") || !strings.Contains(got, "connection refused") {
		t.Fatalf("lost useful context: %q", got)
	}
	if LogSafeError(nil) != "" || LogSafeError(errors.New("plain")) != "plain" {
		t.Fatal("plain errors should pass through")
	}
}
