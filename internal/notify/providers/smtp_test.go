package providers

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
)

var platformSMTP = SMTPConfig{
	Host:     "smtp.platform.example",
	Port:     587,
	Username: "platform-user",
	Password: "platform-secret",
	From:     "noreply@platform.example",
}

type sentMail struct {
	server mailer.Config
	rcpts  []string
	msg    string
}

// recordingSMTP returns a provider whose transport records each send
// instead of dialing.
func recordingSMTP(platform SMTPConfig) (*SMTP, *[]sentMail) {
	var sent []sentMail
	s := NewSMTP(platform, nil)
	s.send = func(cfg mailer.Config, rcpts []string, msg []byte) error {
		sent = append(sent, sentMail{server: cfg, rcpts: rcpts, msg: string(msg)})
		return nil
	}
	return s, &sent
}

func smtpJob(cfg string) domain.NotificationJob {
	return domain.NotificationJob{TargetConfig: cfg, Severity: "critical", MonitorName: "api", Message: "down"}
}

// The attack from notif-1: a platform-mode channel that names a host the
// attacker controls. The provider must not dial it and must not hand the
// platform credentials to anyone but the platform server.
func TestSMTPSend_PlatformModeIgnoresChannelHost(t *testing.T) {
	cases := map[string]string{
		"explicit platform mode": `{"mode":"platform","host":"mail.attacker.example","port":25,"username":"x","password":"y","from":"spoof@attacker.example","to":"ops@org.example"}`,
		"legacy row, no host":    `{"to":"ops@org.example","from":"spoof@attacker.example","username":"x","password":"y"}`,
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			s, sent := recordingSMTP(platformSMTP)
			if _, err := s.Send(context.Background(), smtpJob(cfg)); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if len(*sent) != 1 {
				t.Fatalf("sends = %d, want 1", len(*sent))
			}
			got := (*sent)[0]
			want := mailer.Config{Host: platformSMTP.Host, Port: 587, Username: "platform-user", Password: "platform-secret", From: platformSMTP.From}
			if got.server != want {
				t.Fatalf("server = %+v, want platform server %+v", got.server, want)
			}
			if strings.Contains(got.msg, "attacker") {
				t.Fatalf("message carries channel-supplied server data: %q", got.msg)
			}
			if !reflect.DeepEqual(got.rcpts, []string{"ops@org.example"}) {
				t.Fatalf("rcpts = %v", got.rcpts)
			}
		})
	}
}

func TestSMTPSend_OwnServerNeverGetsPlatformSettings(t *testing.T) {
	s, sent := recordingSMTP(platformSMTP)
	// A legacy smtp-mode row with no credentials: nothing may be filled in
	// from the platform config.
	if _, err := s.Send(context.Background(), smtpJob(`{"host":"mail.attacker.example","port":587,"to":"ops@example.com"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := (*sent)[0].server
	if got.Host != "mail.attacker.example" {
		t.Fatalf("host = %q", got.Host)
	}
	if got.Username != "" || got.Password != "" || got.From == platformSMTP.From {
		t.Fatalf("platform settings leaked to the org server: %+v", got)
	}
}

func TestSMTPSend_OwnServerUsesChannelSettings(t *testing.T) {
	s, sent := recordingSMTP(platformSMTP)
	cfg := `{"mode":"smtp","host":"smtp.org.example","port":465,"tls_mode":"tls","username":"u","password":"p","from":"Alerts <alerts@org.example>","to":"a@org.example, b@org.example"}`
	if _, err := s.Send(context.Background(), smtpJob(cfg)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := (*sent)[0]
	want := mailer.Config{Host: "smtp.org.example", Port: 465, Username: "u", Password: "p", From: "alerts@org.example", TLSMode: "tls"}
	if got.server != want {
		t.Fatalf("server = %+v, want %+v", got.server, want)
	}
	if !reflect.DeepEqual(got.rcpts, []string{"a@org.example", "b@org.example"}) {
		t.Fatalf("rcpts = %v", got.rcpts)
	}
	if !strings.Contains(got.msg, "From: Alerts <alerts@org.example>\r\n") {
		t.Fatalf("From header missing: %q", got.msg)
	}
}

func TestSMTPSend_PlatformModeWithoutPlatformMailerFails(t *testing.T) {
	s, sent := recordingSMTP(SMTPConfig{})
	if _, err := s.Send(context.Background(), smtpJob(`{"to":"ops@org.example"}`)); err == nil {
		t.Fatal("expected an error when no platform mailer is configured")
	}
	if len(*sent) != 0 {
		t.Fatalf("sent %d messages, want 0", len(*sent))
	}
}

func TestSMTPSend_NoRecipientsFails(t *testing.T) {
	s, sent := recordingSMTP(platformSMTP)
	if _, err := s.Send(context.Background(), smtpJob("")); err == nil {
		t.Fatal("expected an error for a channel without recipients")
	}
	if len(*sent) != 0 {
		t.Fatalf("sent %d messages, want 0", len(*sent))
	}
}

func TestSMTPSend_ReadsVersionedEnvelope(t *testing.T) {
	s, sent := recordingSMTP(platformSMTP)
	env := base64.StdEncoding.EncodeToString(append([]byte{0x00}, []byte(`{"mode":"platform","to":"ops@org.example"}`)...))
	if _, err := s.Send(context.Background(), smtpJob(env)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if (*sent)[0].server.Host != platformSMTP.Host {
		t.Fatalf("host = %q", (*sent)[0].server.Host)
	}
}
