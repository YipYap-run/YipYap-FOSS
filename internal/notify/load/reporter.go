package load

import (
	"encoding/json"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// ProviderStats is the JSON-serialisable health snapshot for one provider.
type ProviderStats struct {
	Channel     string  `json:"channel"`
	QueueDepth  int     `json:"queue_depth"`
	Inflight    int64   `json:"inflight"`
	MaxWorkers  int     `json:"max_workers"`
	CapacityPct float64 `json:"capacity_pct"`
	TotalSent   int64   `json:"total_sent"`
	TotalFailed int64   `json:"total_failed"`
}

// Reporter reads pool stats from the Dispatcher and exposes them as JSON.
type Reporter struct {
	dispatcher *notify.Dispatcher
}

// NewReporter creates a reporter that reads from the given dispatcher.
func NewReporter(d *notify.Dispatcher) *Reporter {
	return &Reporter{dispatcher: d}
}

// Stats returns a snapshot of all registered provider pools.
func (r *Reporter) Stats() []ProviderStats {
	pools := r.dispatcher.Pools()
	out := make([]ProviderStats, 0, len(pools))
	for channel, pool := range pools {
		ps := pool.Stats()
		var cap float64
		if ps.MaxWorkers > 0 {
			cap = float64(ps.Inflight) / float64(ps.MaxWorkers) * 100
		}
		out = append(out, ProviderStats{
			Channel:     channel,
			QueueDepth:  ps.QueueDepth,
			Inflight:    ps.Inflight,
			MaxWorkers:  ps.MaxWorkers,
			CapacityPct: cap,
			TotalSent:   ps.TotalSent,
			TotalFailed: ps.TotalFailed,
		})
	}
	return out
}

// HealthHandler returns an http.Handler that serves GET /healthz.
func (r *Reporter) HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Stats())
	})
}
