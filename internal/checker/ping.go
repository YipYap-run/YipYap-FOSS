package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// PingChecker performs ICMP ping checks. It first tries a raw ICMP socket
// (requires root/CAP_NET_RAW), then falls back to executing the system ping command.
type PingChecker struct{}

func (c *PingChecker) Check(ctx context.Context, config json.RawMessage) (*Result, error) {
	var cfg domain.PingCheckConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("ping checker: unmarshal config: %w", err)
	}

	// Try raw ICMP socket first.
	result, err := c.rawPing(ctx, cfg.Host)
	if err == nil {
		return result, nil
	}

	// Fallback to system ping command.
	return c.execPing(ctx, cfg.Host)
}

func (c *PingChecker) rawPing(ctx context.Context, host string) (*Result, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	timeout := time.Until(deadline)

	start := time.Now()
	conn, err := net.DialTimeout("ip4:icmp", host, timeout)
	latency := time.Since(start)

	if err != nil {
		return nil, err // signal to caller to try fallback
	}
	_ = conn.Close()

	return &Result{
		Status:    domain.StatusUp,
		LatencyMS: int(latency.Milliseconds()),
	}, nil
}

func (c *PingChecker) execPing(ctx context.Context, host string) (*Result, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	timeoutSec := int(time.Until(deadline).Seconds())
	if timeoutSec < 1 {
		timeoutSec = 1
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(timeoutSec*1000), host)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(timeoutSec), host)
	}

	start := time.Now()
	err := cmd.Run()
	latency := time.Since(start)

	result := &Result{
		LatencyMS: int(latency.Milliseconds()),
	}

	if err != nil {
		result.Status = domain.StatusDown
		result.Error = fmt.Sprintf("ping failed: %v", err)
		return result, nil
	}

	result.Status = domain.StatusUp
	return result, nil
}
