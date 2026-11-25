package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/emailcfg"
)

// SMTPConfig holds the platform SMTP server settings used by email channels
// in platform mode. Channel configs are parsed by package emailcfg, not into
// this struct.
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// SMTP delivers notifications via email.
type SMTP struct {
	platform SMTPConfig
	decrypt  notify.DecryptFunc
	// send is the transport; tests replace it to observe what would be
	// dialed without opening a connection.
	send func(cfg mailer.Config, rcpts []string, msg []byte) error
}

// NewSMTP creates an SMTP notifier. platform is the platform mailer used by
// platform-mode channels; leave Host empty when there is none.
func NewSMTP(platform SMTPConfig, decrypt notify.DecryptFunc) *SMTP {
	return &SMTP{platform: platform, decrypt: decrypt, send: mailerSend}
}

func mailerSend(cfg mailer.Config, rcpts []string, msg []byte) error {
	m := mailer.New(cfg)
	if m == nil {
		return fmt.Errorf("smtp: host is not configured")
	}
	return m.SendMessageTo(rcpts, msg)
}

func (s *SMTP) Channel() string     { return "smtp" }
func (s *SMTP) MaxConcurrency() int { return 20 }

func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// delivery is the resolved transport for one job.
type delivery struct {
	server     mailer.Config
	headerFrom string
	rcpts      []string
}

// resolve picks the SMTP server and credentials for one job.
//
// Platform mode (the default) reads only the recipients from the channel
// and sends through the platform server with the platform From address.
// Any server settings left in a platform-mode row are ignored, so a
// channel can never point the platform credentials at another host.
//
// SMTP mode uses the channel's own server exactly as stored and never
// reads the platform settings.
func (s *SMTP) resolve(job domain.NotificationJob) (delivery, error) {
	var rawCfg json.RawMessage
	if err := resolveConfig(job, s.decrypt, &rawCfg); err != nil {
		return delivery{}, fmt.Errorf("resolve smtp config: %w", err)
	}
	cfg, err := emailcfg.Resolve(rawCfg)
	if err != nil {
		return delivery{}, fmt.Errorf("smtp config: %w", err)
	}
	rcpts := cfg.Recipients()
	if len(rcpts) == 0 {
		return delivery{}, fmt.Errorf("smtp: channel has no valid recipients")
	}

	if cfg.Mode == emailcfg.ModePlatform {
		if s.platform.Host == "" {
			return delivery{}, fmt.Errorf("smtp: no platform email service is configured; set up your own SMTP server on this channel")
		}
		return delivery{
			server: mailer.Config{
				Host:     s.platform.Host,
				Port:     s.platform.Port,
				Username: s.platform.Username,
				Password: s.platform.Password,
				From:     envelopeAddr(s.platform.From),
			},
			headerFrom: s.platform.From,
			rcpts:      rcpts,
		}, nil
	}

	if cfg.Host == "" {
		return delivery{}, fmt.Errorf("smtp: channel has no SMTP host")
	}
	return delivery{
		server: mailer.Config{
			Host:     cfg.Host,
			Port:     cfg.Port,
			Username: cfg.Username,
			Password: cfg.Password,
			From:     envelopeAddr(cfg.From),
			TLSMode:  cfg.TLSMode,
		},
		headerFrom: cfg.From,
		rcpts:      rcpts,
	}, nil
}

// envelopeAddr returns the bare address for MAIL FROM, accepting a header
// form such as "Alerts <alerts@example.com>".
func envelopeAddr(from string) string {
	if a, err := mail.ParseAddress(from); err == nil {
		return a.Address
	}
	return from
}

func (s *SMTP) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	d, err := s.resolve(job)
	if err != nil {
		return "", err
	}

	subject := fmt.Sprintf("[%s] %s: %s", job.Severity, job.MonitorName, job.Message)
	body := emailBody(job)
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		sanitizeHeader(d.headerFrom), sanitizeHeader(strings.Join(d.rcpts, ", ")), sanitizeHeader(subject), body,
	)

	// The mailer enforces TLS (implicit TLS or a required STARTTLS upgrade)
	// so SMTP credentials never go out in cleartext.
	if err := s.send(d.server, d.rcpts, []byte(msg)); err != nil {
		return "", fmt.Errorf("smtp send: %w", err)
	}
	return "smtp:ok", nil
}
