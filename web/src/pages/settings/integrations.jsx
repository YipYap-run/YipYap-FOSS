// Integrations settings tab.
//
// OpenTelemetry export is not wired yet: the per-org exporter is never
// started (see internal/api/routes_full.go), so this tab must not offer a
// config form or claim metrics are being exported (audit ux-2, owner
// decision core-notify#10). It shows a non-interactive "Coming soon" card.
//
// The API under /api/v1/integrations/otel is ready for when export is
// wired: GET returns {enabled, endpoint, headers_configured, insecure,
// export_active}; PUT takes {endpoint, headers?, clear_headers?, insecure?}
// where a blank or absent headers field keeps stored headers. Only show an
// "exporting" state when export_active is true.

const OTEL_METRICS = [
  ['yipyap.check.latency_ms', 'Monitor check latency'],
  ['yipyap.check.status', 'Monitor up/down state'],
  ['yipyap.alerts.active', 'Currently firing alerts'],
  ['yipyap.notification.sent', 'Notifications the provider accepted'],
  ['yipyap.notification.fail', 'Notifications that failed after every retry'],
  ['yipyap.escalation.step', 'Escalation progression'],
];

export function IntegrationsTab() {
  return (
    <div>
      <section class="card" aria-labelledby="otel-heading">
        <div class="section-header" style="justify-content: flex-start; gap: 8px; flex-wrap: wrap">
          <h3 id="otel-heading">OpenTelemetry Export</h3>
          <span
            class="badge"
            style="border: 1px solid var(--color-border); color: var(--color-text-secondary)"
          >
            Coming soon
          </span>
        </div>

        <p style="color: var(--color-text-secondary); font-size: 0.875rem; margin-bottom: 12px; line-height: 1.6">
          Send your org's monitoring metrics to an OTLP-compatible collector such as
          Grafana Cloud, Honeycomb or Datadog. Export is not available yet, and no
          metrics are sent today.
        </p>

        <p style="color: var(--color-text-secondary); font-size: 0.875rem; margin-bottom: 8px">
          Planned metrics:
        </p>
        <ul style="list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 6px; font-size: 0.8125rem">
          {OTEL_METRICS.map(([name, desc]) => (
            <li key={name} style="display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap">
              <code style="min-width: 220px; overflow-wrap: anywhere">{name}</code>
              <span style="color: var(--color-text-secondary)">{desc}</span>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
