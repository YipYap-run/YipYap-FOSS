package telemetry

import "go.opentelemetry.io/otel/metric"

type Metrics struct {
	CheckLatency     metric.Float64Histogram
	CheckStatus      metric.Int64Gauge
	AlertsActive     metric.Int64UpDownCounter
	NotificationSent metric.Int64Counter
	NotificationFail metric.Int64Counter
	EscalationStep   metric.Int64Gauge

	// Bus metrics.
	BusPublishCount     metric.Int64Counter
	BusConsumeCount     metric.Int64Counter
	BusConsumeLatencyMS metric.Float64Histogram
	BusNackCount        metric.Int64Counter

	// CheckRequestStale counts check.request messages the checker skipped
	// because they were older than about one monitor interval. (core-11)
	CheckRequestStale metric.Int64Counter

	// HTTP metrics.
	HTTPRequestLatencyMS metric.Float64Histogram

	// Business metrics (SaaS only).
	TotalUsers      metric.Int64Gauge
	TotalMonitors   metric.Int64Gauge
	MonitorsByType  metric.Int64Gauge
	CustomersByPlan metric.Int64Gauge

	// Edition-specific metrics, declared in build-tagged files.
	eventingMetrics

	// JWKS metrics (SaaS-only). Safe to read/write with nil checks.
	JWKSActiveKeys       metric.Int64Gauge   // yipyap.jwks.active_keys (active + retiring)
	JWKSTokensSigned     metric.Int64Counter // yipyap.jwks.tokens_signed_total (alg, kid)
	JWKSRotations        metric.Int64Counter // yipyap.jwks.rotations_total (result)
	JWKSActiveKeyAgeSecs metric.Int64Gauge   // yipyap.jwks.active_key_age_seconds
}

func NewMetrics(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{}
	var err error

	m.CheckLatency, err = meter.Float64Histogram("yipyap.check.latency_ms",
		metric.WithDescription("Check latency in milliseconds"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}

	m.CheckStatus, err = meter.Int64Gauge("yipyap.check.status",
		metric.WithDescription("Check status: 1=up, 0=down per monitor"))
	if err != nil {
		return nil, err
	}

	m.AlertsActive, err = meter.Int64UpDownCounter("yipyap.alerts.active",
		metric.WithDescription("Number of currently active alerts"))
	if err != nil {
		return nil, err
	}

	m.NotificationSent, err = meter.Int64Counter("yipyap.notification.sent",
		metric.WithDescription("Notifications the provider accepted (not proof the recipient got them)"))
	if err != nil {
		return nil, err
	}

	m.NotificationFail, err = meter.Int64Counter("yipyap.notification.fail",
		metric.WithDescription("Notifications that failed after every retry"))
	if err != nil {
		return nil, err
	}

	m.EscalationStep, err = meter.Int64Gauge("yipyap.escalation.step",
		metric.WithDescription("Current escalation step per alert"))
	if err != nil {
		return nil, err
	}

	m.BusPublishCount, err = meter.Int64Counter("yipyap.bus.publish.count",
		metric.WithDescription("Messages published per subject"))
	if err != nil {
		return nil, err
	}

	m.BusConsumeCount, err = meter.Int64Counter("yipyap.bus.consume.count",
		metric.WithDescription("Messages consumed per subject"))
	if err != nil {
		return nil, err
	}

	m.BusConsumeLatencyMS, err = meter.Float64Histogram("yipyap.bus.consume.latency_ms",
		metric.WithDescription("Time from publish to handler start"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}

	m.BusNackCount, err = meter.Int64Counter("yipyap.bus.nack.count",
		metric.WithDescription("Messages nacked for redelivery"))
	if err != nil {
		return nil, err
	}

	m.CheckRequestStale, err = meter.Int64Counter("yipyap.checker.request_stale.count",
		metric.WithDescription("check.request messages skipped because they were older than one monitor interval"))
	if err != nil {
		return nil, err
	}

	m.HTTPRequestLatencyMS, err = meter.Float64Histogram("yipyap.http.request.latency_ms",
		metric.WithDescription("API endpoint latency"),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}

	m.TotalUsers, err = meter.Int64Gauge("yipyap.users.total",
		metric.WithDescription("Total registered users"))
	if err != nil {
		return nil, err
	}

	m.TotalMonitors, err = meter.Int64Gauge("yipyap.monitors.total",
		metric.WithDescription("Total monitors"))
	if err != nil {
		return nil, err
	}

	m.MonitorsByType, err = meter.Int64Gauge("yipyap.monitors.by_type",
		metric.WithDescription("Monitor count by type"))
	if err != nil {
		return nil, err
	}

	m.CustomersByPlan, err = meter.Int64Gauge("yipyap.customers.by_plan",
		metric.WithDescription("Organization count by plan tier"))
	if err != nil {
		return nil, err
	}

	if err := m.initEventingMetrics(meter); err != nil {
		return nil, err
	}

	m.JWKSActiveKeys, err = meter.Int64Gauge("yipyap.jwks.active_keys",
		metric.WithDescription("JWKS keys currently advertised (active + retiring)"))
	if err != nil {
		return nil, err
	}
	m.JWKSTokensSigned, err = meter.Int64Counter("yipyap.jwks.tokens_signed_total",
		metric.WithDescription("Total JWTs signed by the JWKS subsystem (labels: alg, kid)"))
	if err != nil {
		return nil, err
	}
	m.JWKSRotations, err = meter.Int64Counter("yipyap.jwks.rotations_total",
		metric.WithDescription("Total JWKS rotation attempts (labels: result=success|failure)"))
	if err != nil {
		return nil, err
	}
	m.JWKSActiveKeyAgeSecs, err = meter.Int64Gauge("yipyap.jwks.active_key_age_seconds",
		metric.WithDescription("Age in seconds of the current active JWKS signing key"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	return m, nil
}
