package mailer

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

func TestNew_NilWhenUnconfigured(t *testing.T) {
	m := New(Config{})
	if m != nil {
		t.Fatal("expected nil mailer when host is empty")
	}
}

func TestNew_DefaultPort(t *testing.T) {
	m := New(Config{Host: "smtp.example.com"})
	if m.cfg.Port != 587 {
		t.Errorf("expected default port 587, got %d", m.cfg.Port)
	}
}

func TestNew_DefaultFrom(t *testing.T) {
	m := New(Config{Host: "smtp.example.com"})
	if m.cfg.From != "noreply@localhost" {
		t.Errorf("expected default from noreply@localhost, got %s", m.cfg.From)
	}
}

func TestNew_CustomConfig(t *testing.T) {
	m := New(Config{Host: "mail.co", Port: 465, From: "hi@co.com"})
	if m.cfg.Port != 465 {
		t.Errorf("expected port 465, got %d", m.cfg.Port)
	}
	if m.cfg.From != "hi@co.com" {
		t.Errorf("expected from hi@co.com, got %s", m.cfg.From)
	}
}

// plainSMTPServer accepts connections and speaks just enough cleartext SMTP
// to answer EHLO without offering STARTTLS.
func plainSMTPServer(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = c.Write([]byte("220 test ESMTP\r\n"))
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if strings.HasPrefix(strings.ToUpper(line), "QUIT") {
						_, _ = c.Write([]byte("221 bye\r\n"))
						return
					}
					_, _ = c.Write([]byte("250 test\r\n"))
				}
			}(c)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func TestSendMessageTo_TLSModeSelectsTransport(t *testing.T) {
	host, port := plainSMTPServer(t)

	m := New(Config{Host: host, Port: port, TLSMode: "starttls"})
	err := m.SendMessageTo([]string{"a@example.com"}, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "does not support STARTTLS") {
		t.Fatalf("starttls: err = %v, want a STARTTLS refusal", err)
	}

	m = New(Config{Host: host, Port: port, TLSMode: "tls"})
	err = m.SendMessageTo([]string{"a@example.com"}, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "smtps dial") {
		t.Fatalf("tls: err = %v, want an implicit TLS handshake failure", err)
	}
}

func TestSendMessageTo_NoRecipients(t *testing.T) {
	m := New(Config{Host: "smtp.example.com"})
	if err := m.SendMessageTo(nil, []byte("x")); err == nil {
		t.Fatal("expected an error with no recipients")
	}
}
