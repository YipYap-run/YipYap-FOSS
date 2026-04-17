package mailer

import "testing"

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
	if m.cfg.From != "noreply@yipyap.run" {
		t.Errorf("expected default from noreply@yipyap.run, got %s", m.cfg.From)
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
