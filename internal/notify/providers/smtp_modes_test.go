package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
)

// smtpServerKeys are the server settings a platform-mode email channel
// must not be able to set (notif-1, notif-18), with values that are easy
// to spot in the dialed config or the message.
var smtpServerKeys = map[string]any{
	"host":     "mail.attacker.example",
	"HOST":     "mail.attacker.example",
	"port":     2525,
	"tls_mode": "none",
	"username": "attacker-user",
	"Username": "attacker-user",
	"password": "attacker-pass",
	"from":     "spoof@attacker.example",
	"From":     "spoof@attacker.example",
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func smtpCfgJSON(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var wantPlatformServer = mailer.Config{
	Host: platformSMTP.Host, Port: platformSMTP.Port, Username: platformSMTP.Username,
	Password: platformSMTP.Password, From: platformSMTP.From,
}

// Each server key on its own, in an explicit platform-mode row, must leave
// the dialed server and the message headers exactly as platform config says.
func TestSMTPSend_PlatformModeIgnoresEachServerKey(t *testing.T) {
	for _, key := range sortedKeys(smtpServerKeys) {
		t.Run(key, func(t *testing.T) {
			s, sent := recordingSMTP(platformSMTP)
			cfg := smtpCfgJSON(t, map[string]any{"mode": "platform", "to": "ops@org.example", key: smtpServerKeys[key]})
			if _, err := s.Send(context.Background(), smtpJob(cfg)); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if len(*sent) != 1 {
				t.Fatalf("sends = %d, want 1", len(*sent))
			}
			got := (*sent)[0]
			if got.server != wantPlatformServer {
				t.Fatalf("server = %+v, want %+v", got.server, wantPlatformServer)
			}
			if strings.Contains(got.msg, "attacker") {
				t.Fatalf("message carries a channel server value: %q", got.msg)
			}
			if !strings.HasPrefix(got.msg, "From: "+platformSMTP.From+"\r\n") {
				t.Fatalf("From header is not the platform address: %q", got.msg)
			}
		})
	}
}

// In own-server mode, a setting the channel left out stays empty. The
// platform's credentials, sender and TLS choice are never filled in, so an
// org server never receives them.
func TestSMTPSend_OwnServerMissingFieldNotFilledFromPlatform(t *testing.T) {
	full := map[string]any{
		"mode": "smtp", "host": "smtp.org.example", "port": 587, "tls_mode": "starttls",
		"username": "org-user", "password": "org-pass", "from": "alerts@org.example", "to": "ops@org.example",
	}
	for _, drop := range []string{"port", "tls_mode", "username", "password", "from"} {
		t.Run("without "+drop, func(t *testing.T) {
			cfg := map[string]any{}
			for k, v := range full {
				if k != drop {
					cfg[k] = v
				}
			}
			s, sent := recordingSMTP(platformSMTP)
			if _, err := s.Send(context.Background(), smtpJob(smtpCfgJSON(t, cfg))); err != nil {
				t.Fatalf("Send: %v", err)
			}
			got := (*sent)[0]
			if got.server.Host != "smtp.org.example" {
				t.Fatalf("host = %q, want the org server", got.server.Host)
			}
			if got.server.Username == platformSMTP.Username || got.server.Password == platformSMTP.Password ||
				got.server.From == platformSMTP.From || strings.Contains(got.msg, platformSMTP.From) {
				t.Fatalf("platform settings reached the org server: %+v", got.server)
			}
		})
	}
}

// A platform-mode row with no platform mailer configured fails. It must
// not try the org server keys left in the row instead.
func TestSMTPSend_PlatformModeNeverFallsBackToChannelServer(t *testing.T) {
	s, sent := recordingSMTP(SMTPConfig{})
	cfg := `{"mode":"platform","host":"mail.org.example","port":587,"username":"u","password":"p","from":"a@org.example","to":"ops@org.example"}`
	if _, err := s.Send(context.Background(), smtpJob(cfg)); err == nil {
		t.Fatal("expected an error without a platform mailer")
	}
	if len(*sent) != 0 {
		t.Fatalf("sent %d messages, want 0", len(*sent))
	}
}

// The encrypted (0x01) envelope goes through the same mode rules.
func TestSMTPSend_EncryptedEnvelopeModes(t *testing.T) {
	enc := func(cfg string) string {
		b := []byte(cfg)
		out := make([]byte, len(b))
		for i, c := range b {
			out[i] = c ^ 0x5a
		}
		return base64.StdEncoding.EncodeToString(append([]byte{0x01}, out...))
	}
	decrypt := func(b []byte) ([]byte, error) {
		out := make([]byte, len(b))
		for i, c := range b {
			out[i] = c ^ 0x5a
		}
		return out, nil
	}
	cases := map[string]struct {
		cfg  string
		want mailer.Config
	}{
		"platform": {
			cfg:  `{"mode":"platform","host":"mail.attacker.example","username":"x","password":"y","to":"ops@org.example"}`,
			want: wantPlatformServer,
		},
		"own server": {
			cfg:  `{"mode":"smtp","host":"smtp.org.example","port":465,"tls_mode":"tls","username":"u","password":"p","from":"a@org.example","to":"ops@org.example"}`,
			want: mailer.Config{Host: "smtp.org.example", Port: 465, TLSMode: "tls", Username: "u", Password: "p", From: "a@org.example"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, sent := recordingSMTP(platformSMTP)
			s.decrypt = decrypt
			if _, err := s.Send(context.Background(), smtpJob(enc(tc.cfg))); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if got := (*sent)[0].server; got != tc.want {
				t.Fatalf("server = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("no decrypt function", func(t *testing.T) {
		s, sent := recordingSMTP(platformSMTP)
		if _, err := s.Send(context.Background(), smtpJob(enc(`{"to":"ops@org.example"}`))); err == nil {
			t.Fatal("expected an error")
		}
		if len(*sent) != 0 {
			t.Fatalf("sent %d messages, want 0", len(*sent))
		}
	})
}
