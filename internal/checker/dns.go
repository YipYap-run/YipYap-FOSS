package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// DNSMetadata is stored in Result.Metadata as JSON so that the scheduler can
// compare resolved records between consecutive checks and detect changes.
type DNSMetadata struct {
	RecordType string   `json:"record_type"`
	Records    []string `json:"records"`
}

// DNSChecker performs DNS resolution checks.
type DNSChecker struct{}

func (c *DNSChecker) Check(ctx context.Context, config json.RawMessage) (*Result, error) {
	var cfg domain.DNSCheckConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("dns checker: unmarshal config: %w", err)
	}

	if cfg.RecordType == "" {
		cfg.RecordType = "A"
	}

	resolver := &net.Resolver{
		PreferGo: true,
	}
	if cfg.Nameserver != "" {
		resolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", cfg.Nameserver+":53")
		}
	}

	start := time.Now()
	records, err := c.resolve(ctx, resolver, cfg.Hostname, cfg.RecordType)
	latency := time.Since(start)

	result := &Result{
		LatencyMS: int(latency.Milliseconds()),
	}

	if err != nil {
		result.Status = domain.StatusDown
		result.Error = err.Error()
		return result, nil
	}

	if len(records) == 0 {
		result.Status = domain.StatusDown
		result.Error = "no records returned"
		return result, nil
	}

	// Normalise and sort records for stable comparison across checks.
	normalised := make([]string, len(records))
	for i, r := range records {
		normalised[i] = strings.TrimSuffix(strings.ToLower(r), ".")
	}
	sort.Strings(normalised)

	// Store resolved records as metadata for change detection.
	meta := DNSMetadata{
		RecordType: strings.ToUpper(cfg.RecordType),
		Records:    normalised,
	}
	if data, err := json.Marshal(meta); err == nil {
		result.Metadata = string(data)
	}

	// Check expected value if configured.
	if cfg.Expected != "" {
		found := false
		expected := strings.TrimSuffix(strings.ToLower(cfg.Expected), ".")
		for _, r := range normalised {
			if r == expected {
				found = true
				break
			}
		}
		if !found {
			result.Status = domain.StatusDown
			result.Error = fmt.Sprintf("expected %q not found in results: %v", cfg.Expected, records)
			return result, nil
		}
	}

	result.Status = domain.StatusUp
	return result, nil
}

func (c *DNSChecker) resolve(ctx context.Context, resolver *net.Resolver, hostname, recordType string) ([]string, error) {
	switch strings.ToUpper(recordType) {
	case "A":
		ips, err := resolver.LookupHost(ctx, hostname)
		if err != nil {
			return nil, err
		}
		// Filter to IPv4 only for A records.
		var results []string
		for _, ip := range ips {
			if net.ParseIP(ip).To4() != nil {
				results = append(results, ip)
			}
		}
		return results, nil

	case "AAAA":
		ips, err := resolver.LookupHost(ctx, hostname)
		if err != nil {
			return nil, err
		}
		// Filter to IPv6 only.
		var results []string
		for _, ip := range ips {
			if net.ParseIP(ip).To4() == nil {
				results = append(results, ip)
			}
		}
		return results, nil

	case "CNAME":
		cname, err := resolver.LookupCNAME(ctx, hostname)
		if err != nil {
			return nil, err
		}
		return []string{cname}, nil

	case "MX":
		mxs, err := resolver.LookupMX(ctx, hostname)
		if err != nil {
			return nil, err
		}
		var results []string
		for _, mx := range mxs {
			results = append(results, mx.Host)
		}
		return results, nil

	case "TXT":
		txts, err := resolver.LookupTXT(ctx, hostname)
		if err != nil {
			return nil, err
		}
		return txts, nil

	default:
		return nil, fmt.Errorf("unsupported record type: %s", recordType)
	}
}
