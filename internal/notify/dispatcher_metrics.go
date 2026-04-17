package notify

// NewDispatcherMetrics returns nil in FOSS builds (no telemetry).
func NewDispatcherMetrics(_ any) DispatcherMetrics {
	return nil
}
