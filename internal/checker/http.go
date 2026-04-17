package checker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// sharedTransport is a connection-pooling HTTP transport reused across all
// HTTP checks. This avoids per-check TCP+TLS handshake overhead by keeping
// idle connections alive for repeat checks to the same host.
var sharedTransport = &http.Transport{
	TLSClientConfig: &tls.Config{
		InsecureSkipVerify: false,
	},
	MaxIdleConns:        2048,
	MaxIdleConnsPerHost: 128,
	IdleConnTimeout:     120 * time.Second,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	TLSHandshakeTimeout: 10 * time.Second,
}

// sharedClient reuses the pooled transport. One client for all checks avoids
// per-request allocation of transport state.
var sharedClient = &http.Client{
	Transport: sharedTransport,
	// Don't follow redirects automatically so we capture the actual status code.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// HTTPChecker performs HTTP health checks.
type HTTPChecker struct{}

func (c *HTTPChecker) Check(ctx context.Context, config json.RawMessage) (*Result, error) {
	var cfg domain.HTTPCheckConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("http checker: unmarshal config: %w", err)
	}

	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}
	if cfg.ExpectedStatus == 0 {
		cfg.ExpectedStatus = http.StatusOK
	}

	allowedMethods := map[string]bool{
		http.MethodGet:     true,
		http.MethodPost:    true,
		http.MethodPut:     true,
		http.MethodPatch:   true,
		http.MethodDelete:  true,
		http.MethodHead:    true,
		http.MethodOptions: true,
	}
	if !allowedMethods[strings.ToUpper(cfg.Method)] {
		return nil, fmt.Errorf("http checker: unsupported method %q", cfg.Method)
	}
	cfg.Method = strings.ToUpper(cfg.Method)

	var bodyReader io.Reader
	if cfg.Body != "" {
		bodyReader = strings.NewReader(cfg.Body)
	}

	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("http checker: create request: %w", err)
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := sharedClient.Do(req)
	latency := time.Since(start)

	result := &Result{
		LatencyMS: int(latency.Milliseconds()),
	}

	if err != nil {
		result.Status = domain.StatusDown
		result.Error = err.Error()
		return result, nil
	}
	defer func() { _ = resp.Body.Close() }()

	result.StatusCode = resp.StatusCode

	// Read TLS certificate expiry.
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		expiry := resp.TLS.PeerCertificates[0].NotAfter
		result.TLSExpiry = &expiry
	}

	// Check status code.
	if resp.StatusCode != cfg.ExpectedStatus {
		result.Status = domain.StatusDown
		result.Error = fmt.Sprintf("expected status %d, got %d", cfg.ExpectedStatus, resp.StatusCode)
		return result, nil
	}

	// Check body match if configured.
	if cfg.BodyMatch != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // limit to 1MB
		if err != nil {
			result.Status = domain.StatusDown
			result.Error = fmt.Sprintf("reading body: %v", err)
			return result, nil
		}
		if !strings.Contains(string(body), cfg.BodyMatch) {
			result.Status = domain.StatusDown
			result.Error = fmt.Sprintf("body does not contain %q", cfg.BodyMatch)
			return result, nil
		}
	}

	result.Status = domain.StatusUp
	return result, nil
}
