package checker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// Result holds the outcome of a single monitor check.
type Result struct {
	Status     domain.CheckStatus
	LatencyMS  int
	StatusCode int
	Error      string
	Metadata   string
	TLSExpiry  *time.Time
}

// Checker performs a health check given a raw JSON configuration.
type Checker interface {
	Check(ctx context.Context, config json.RawMessage) (*Result, error)
}

// ForType returns the appropriate Checker implementation for the given monitor type.
func ForType(t domain.MonitorType) Checker {
	switch t {
	case domain.MonitorHTTP:
		return &HTTPChecker{}
	case domain.MonitorTCP:
		return &TCPChecker{}
	case domain.MonitorPing:
		return &PingChecker{}
	case domain.MonitorDNS:
		return &DNSChecker{}
	default:
		return nil
	}
}
