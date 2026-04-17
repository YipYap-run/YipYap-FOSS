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

	// HTTP metrics.
	HTTPRequestLatencyMS metric.Float64Histogram

	// Business metrics (SaaS only).
	TotalUsers       metric.Int64Gauge
	TotalMonitors    metric.Int64Gauge
	MonitorsByType   metric.Int64Gauge
	CustomersByPlan  metric.Int64Gauge
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
		metric.WithDescription("Total notifications sent successfully"))
	if err != nil {
		return nil, err
	}

	m.NotificationFail, err = meter.Int64Counter("yipyap.notification.fail",
		metric.WithDescription("Total notification send failures"))
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

	return m, nil
}
