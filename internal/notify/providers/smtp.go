package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// SMTPConfig holds SMTP server settings.
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
	fallback SMTPConfig
	decrypt  notify.DecryptFunc
}

// NewSMTP creates an SMTP notifier.
func NewSMTP(fallback SMTPConfig, decrypt notify.DecryptFunc) *SMTP {
	return &SMTP{fallback: fallback, decrypt: decrypt}
}

func (s *SMTP) Channel() string     { return "smtp" }
func (s *SMTP) MaxConcurrency() int { return 20 }

func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func (s *SMTP) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := s.fallback
	if err := resolveConfig(job, s.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve smtp config: %w", err)
	}

	subject := fmt.Sprintf("[%s] %s: %s", job.Severity, job.MonitorName, job.Message)
	body := fmt.Sprintf(
		"Alert ID: %s\nMonitor: %s\nSeverity: %s\n\n%s\n\nAcknowledge: %s",
		job.AlertID, job.MonitorName, job.Severity, job.Message, job.AckURL,
	)
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		sanitizeHeader(cfg.From), sanitizeHeader(cfg.To), sanitizeHeader(subject), body,
	)

	// Reuse the mailer's TLS-enforcing send (SMTPS on 465, required STARTTLS
	// otherwise) so SMTP credentials never go out in cleartext.
	m := mailer.New(mailer.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: cfg.Password,
		From:     cfg.From,
	})
	if m == nil {
		return "", fmt.Errorf("smtp: host is not configured")
	}
	if err := m.SendMessage(cfg.To, []byte(msg)); err != nil {
		return "", fmt.Errorf("smtp send: %w", err)
	}
	return "smtp:ok", nil
}
