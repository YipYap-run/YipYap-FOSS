package escalation

// NewEngineMetrics returns nil in FOSS builds (no telemetry).
func NewEngineMetrics(_ any) EngineMetrics {
	return nil
}
