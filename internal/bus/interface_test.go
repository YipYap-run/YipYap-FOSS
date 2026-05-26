package bus_test

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
)

func TestMsg_FieldsAccessible(t *testing.T) {
	acked := false
	naked := false

	m := &bus.Msg{
		Subject: "test.subject",
		Data:    []byte("hello"),
		Headers: map[string]string{"X-Trace": "abc123"},
		MsgID:   "msg-1",
		Ack:     func() error { acked = true; return nil },
		Nak:     func() error { naked = true; return nil },
	}

	if m.Subject != "test.subject" {
		t.Fatalf("expected subject test.subject, got %s", m.Subject)
	}
	if string(m.Data) != "hello" {
		t.Fatalf("expected data hello, got %s", m.Data)
	}
	if m.Headers["X-Trace"] != "abc123" {
		t.Fatalf("expected header abc123, got %s", m.Headers["X-Trace"])
	}
	if m.MsgID != "msg-1" {
		t.Fatalf("expected MsgID msg-1, got %s", m.MsgID)
	}

	if err := m.Ack(); err != nil {
		t.Fatalf("Ack returned error: %v", err)
	}
	if !acked {
		t.Fatal("Ack was not called")
	}

	if err := m.Nak(); err != nil {
		t.Fatalf("Nak returned error: %v", err)
	}
	if !naked {
		t.Fatal("Nak was not called")
	}
}

func TestPullConfig_Defaults(t *testing.T) {
	cfg := bus.DefaultPullConfig()

	if cfg.MaxDeliver != 5 {
		t.Fatalf("expected MaxDeliver=5, got %d", cfg.MaxDeliver)
	}
	if cfg.AckWait != 30*time.Second {
		t.Fatalf("expected AckWait=30s, got %v", cfg.AckWait)
	}
	if cfg.BatchSize != 10 {
		t.Fatalf("expected BatchSize=10, got %d", cfg.BatchSize)
	}
}

func TestPullConfig_WithMaxDeliver(t *testing.T) {
	cfg := bus.DefaultPullConfig(bus.WithMaxDeliver(10))
	if cfg.MaxDeliver != 10 {
		t.Fatalf("expected MaxDeliver=10, got %d", cfg.MaxDeliver)
	}
}

func TestPullConfig_WithAckWait(t *testing.T) {
	cfg := bus.DefaultPullConfig(bus.WithAckWait(60 * time.Second))
	if cfg.AckWait != 60*time.Second {
		t.Fatalf("expected AckWait=60s, got %v", cfg.AckWait)
	}
}

func TestPullConfig_WithBatchSize(t *testing.T) {
	cfg := bus.DefaultPullConfig(bus.WithBatchSize(25))
	if cfg.BatchSize != 25 {
		t.Fatalf("expected BatchSize=25, got %d", cfg.BatchSize)
	}
}

func TestPullConfig_MultipleOpts(t *testing.T) {
	cfg := bus.DefaultPullConfig(
		bus.WithMaxDeliver(3),
		bus.WithAckWait(10*time.Second),
		bus.WithBatchSize(50),
	)
	if cfg.MaxDeliver != 3 {
		t.Fatalf("expected MaxDeliver=3, got %d", cfg.MaxDeliver)
	}
	if cfg.AckWait != 10*time.Second {
		t.Fatalf("expected AckWait=10s, got %v", cfg.AckWait)
	}
	if cfg.BatchSize != 50 {
		t.Fatalf("expected BatchSize=50, got %d", cfg.BatchSize)
	}
}

// TestAckHandler_Signature ensures AckHandler matches the expected signature.
func TestAckHandler_Signature(t *testing.T) {
	var h bus.AckHandler = func(ctx context.Context, msg *bus.Msg) error {
		return msg.Ack()
	}

	m := &bus.Msg{
		Ack: func() error { return nil },
	}
	if err := h(context.Background(), m); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
}
