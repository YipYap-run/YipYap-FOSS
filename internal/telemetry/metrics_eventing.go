package telemetry

import "go.opentelemetry.io/otel/metric"

// eventingMetrics is empty in this build.
type eventingMetrics struct{}

func (m *Metrics) initEventingMetrics(_ metric.Meter) error {
	m.eventingMetrics = eventingMetrics{}
	return nil
}
