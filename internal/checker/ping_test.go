package checker

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/icmp"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// noPingBinary simulates the scratch release images, which have no ping
// command, so only the in-process ICMP path can answer.
func noPingBinary(t *testing.T) {
	t.Helper()
	old := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { lookPath = old })
}

// requireUnprivilegedICMP skips when this host does not allow an ICMP
// datagram socket for the test user (Linux ping_group_range excludes it).
func requireUnprivilegedICMP(t *testing.T) {
	t.Helper()
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		t.Skipf("unprivileged ICMP not allowed here: %v", err)
	}
	_ = c.Close()
}

func pingCheck(t *testing.T, host string, timeout time.Duration) *Result {
	t.Helper()
	cfg, _ := json.Marshal(domain.PingCheckConfig{Host: host})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := (&PingChecker{}).Check(ctx, cfg)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res
}

// echoCheck runs the in-process ICMP path directly. The tests use loopback
// and TEST-NET targets, which the monitor target guard (mon-4) refuses in
// Check, so they exercise echo rather than the whole Check.
func echoCheck(t *testing.T, host string, timeout time.Duration) *Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	res, err := (&PingChecker{}).echo(ctx, host, deadline)
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	return res
}

// As a non-root user with no ping binary (the core-19 release images), a
// reachable host must report up. The old checker only tried a raw socket and
// the ping command, so it reported every ping monitor down there.
func TestPingLoopbackUpWithoutRoot(t *testing.T) {
	requireUnprivilegedICMP(t)
	res := echoCheck(t, "127.0.0.1", 3*time.Second)
	if res.Status != domain.StatusUp {
		t.Fatalf("status = %s (%s), want up", res.Status, res.Error)
	}
}

// A host that never answers must report down. The old raw-socket path
// returned up as soon as the socket opened, without sending anything.
func TestPingUnansweredHostIsDown(t *testing.T) {
	requireUnprivilegedICMP(t)
	// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): never routed.
	start := time.Now()
	res := echoCheck(t, "192.0.2.1", 700*time.Millisecond)
	if res.Status != domain.StatusDown {
		t.Fatalf("status = %s, want down", res.Status)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("check ignored the context deadline: took %s", time.Since(start))
	}
}

func TestPingNoSocketAndNoBinaryExplainsFix(t *testing.T) {
	noPingBinary(t)
	old := pingSocketsV4
	pingSocketsV4 = []pingSocket{{"udp4", "256.0.0.1", true}} // cannot bind
	t.Cleanup(func() { pingSocketsV4 = old })

	// A public literal: nothing is sent because no socket opens.
	res := pingCheck(t, "1.1.1.1", time.Second)
	if res.Status != domain.StatusDown || !strings.Contains(res.Error, "ping_group_range") {
		t.Fatalf("got %s %q, want down with a ping_group_range hint", res.Status, res.Error)
	}
}

func TestPingEmptyHostIsDown(t *testing.T) {
	res := pingCheck(t, "", time.Second)
	if res.Status != domain.StatusDown {
		t.Fatalf("status = %s, want down", res.Status)
	}
}

func TestPingEchoReportsSocketFailure(t *testing.T) {
	old := pingSocketsV4
	pingSocketsV4 = []pingSocket{{"udp4", "256.0.0.1", true}}
	t.Cleanup(func() { pingSocketsV4 = old })
	_, err := (&PingChecker{}).echo(context.Background(), "127.0.0.1", time.Now().Add(time.Second))
	if !errors.Is(err, errNoICMPSocket) {
		t.Fatalf("err = %v, want errNoICMPSocket", err)
	}
}
