package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// TCPChecker performs TCP connectivity checks.
type TCPChecker struct{}

func (c *TCPChecker) Check(ctx context.Context, config json.RawMessage) (*Result, error) {
	var cfg domain.TCPCheckConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("tcp checker: unmarshal config: %w", err)
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	// Extract deadline from context if present, otherwise use 10s default.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	timeout := time.Until(deadline)

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	latency := time.Since(start)

	result := &Result{
		LatencyMS: int(latency.Milliseconds()),
	}

	if err != nil {
		result.Status = domain.StatusDown
		result.Error = err.Error()
		return result, nil
	}
	_ = conn.Close()

	result.Status = domain.StatusUp
	return result, nil
}
