package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

type Mailer struct {
	cfg Config
}

func New(cfg Config) *Mailer {
	if cfg.Host == "" {
		return nil
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.From == "" {
		cfg.From = "noreply@yipyap.run"
	}
	return &Mailer{cfg: cfg}
}

func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func (m *Mailer) Send(to, subject, body string) error {
	msg := fmt.Sprintf(
		"From: YipYap <%s>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		sanitizeHeader(m.cfg.From), sanitizeHeader(to), sanitizeHeader(subject), body,
	)

	done := make(chan error, 1)
	go func() {
		if m.cfg.Port == 465 {
			done <- m.sendSMTPS(to, []byte(msg))
		} else {
			done <- m.sendSTARTTLS(to, []byte(msg))
		}
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("smtp send timed out")
	}
}

// sendSMTPS sends mail over an implicit TLS connection (port 465 / SMTPS).
// The TLS handshake is performed before any SMTP traffic so credentials are
// never sent in cleartext.
func (m *Mailer) sendSMTPS(to string, msg []byte) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	tlsCfg := &tls.Config{
		ServerName: m.cfg.Host,
		MinVersion: tls.VersionTLS12,
	}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("smtps dial: %w", err)
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtps new client: %w", err)
	}
	defer func() { _ = client.Quit() }()

	if m.cfg.Username != "" {
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtps auth: %w", err)
		}
	}

	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtps MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtps RCPT TO: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtps DATA: %w", err)
	}
	if _, err = wc.Write(msg); err != nil {
		return fmt.Errorf("smtps write: %w", err)
	}
	return wc.Close()
}

// sendSTARTTLS sends mail over a plain TCP connection that is upgraded to TLS
// via STARTTLS before any authentication takes place (port 587).  If the server
// does not advertise STARTTLS, the send is aborted rather than falling back to
// cleartext.
func (m *Mailer) sendSTARTTLS(to string, msg []byte) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))

	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer func() { _ = client.Quit() }()

	// Enforce STARTTLS  - reject if the server does not support it.
	ok, _ := client.Extension("STARTTLS")
	if !ok {
		return fmt.Errorf("smtp server %s does not support STARTTLS; refusing to send credentials in cleartext", m.cfg.Host)
	}
	tlsCfg := &tls.Config{
		ServerName: m.cfg.Host,
		MinVersion: tls.VersionTLS12,
	}
	if err := client.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("smtp STARTTLS: %w", err)
	}

	if m.cfg.Username != "" {
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err = wc.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	return wc.Close()
}
