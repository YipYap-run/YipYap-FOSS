package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// OrgSettingsReader is the subset of store.OrgSettingsStore needed here.
// Using a local interface avoids a circular import with internal/store.
type OrgSettingsReader interface {
	Get(ctx context.Context, orgID, key string) (string, error)
	GetAll(ctx context.Context, orgID string) (map[string]string, error)
}

// OrgExporterManager maintains per-org OTEL MeterProviders, each exporting
// only that org's metrics to their configured OTLP endpoint.
type OrgExporterManager struct {
	mu        sync.RWMutex
	exporters map[string]*orgExporter // org_id -> exporter
	settings  OrgSettingsReader
	decrypt   func(ciphertext string) (string, error)
}

type orgExporter struct {
	provider *sdkmetric.MeterProvider
	meter    metric.Meter
	endpoint string
}

const orgExportInterval = 60 * time.Second

// NewOrgExporterManager creates a manager that maintains per-org OTEL exporters.
// The decrypt function wraps crypto.Envelope.Decrypt for header values.
func NewOrgExporterManager(settings OrgSettingsReader, decrypt func(string) (string, error)) *OrgExporterManager {
	return &OrgExporterManager{
		exporters: make(map[string]*orgExporter),
		settings:  settings,
		decrypt:   decrypt,
	}
}

// GetMeter returns the OTEL Meter for a specific org. Returns nil if the
// org has no OTEL export configured.
func (m *OrgExporterManager) GetMeter(orgID string) metric.Meter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	exp, ok := m.exporters[orgID]
	if !ok {
		return nil
	}
	return exp.meter
}

// SyncOrg creates, updates, or removes the OTLP exporter for a single org
// based on its current org_settings.
func (m *OrgExporterManager) SyncOrg(ctx context.Context, orgID string) error {
	settings, err := m.settings.GetAll(ctx, orgID)
	if err != nil {
		return err
	}

	endpoint := settings["otel_endpoint"]

	// Check current state under read lock.
	m.mu.RLock()
	existing, exists := m.exporters[orgID]
	endpointChanged := !exists || existing.endpoint != endpoint
	m.mu.RUnlock()

	// No endpoint configured -- remove exporter if it exists.
	if endpoint == "" {
		if exists {
			m.mu.Lock()
			// Re-check under write lock.
			existing, exists = m.exporters[orgID]
			if exists {
				delete(m.exporters, orgID)
			}
			m.mu.Unlock()
			if exists {
				if err := existing.provider.Shutdown(ctx); err != nil {
					slog.Warn("otel: shutdown error during sync", "org_id", orgID, "error", err)
				}
				slog.Info("otel: removed per-org exporter", "org_id", orgID)
			}
		}
		return nil
	}

	// Endpoint unchanged -- nothing to do.
	if !endpointChanged {
		return nil
	}

	// Decrypt headers if present.
	headers := ""
	if raw, ok := settings["otel_headers"]; ok && raw != "" {
		if m.decrypt != nil {
			decrypted, err := m.decrypt(raw)
			if err != nil {
				slog.Warn("otel: failed to decrypt headers", "org_id", orgID, "error", err)
			} else {
				headers = decrypted
			}
		}
	}

	// Create OTLP exporter with org-specific endpoint OUTSIDE the lock.
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithInsecure(), // TODO: configurable TLS
	}
	if headers != "" {
		opts = append(opts, otlpmetricgrpc.WithHeaders(parseHeaders(headers)))
	}

	metricExp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("otel exporter for org %s: %w", orgID, err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(orgExportInterval)),
		),
	)

	newExp := &orgExporter{
		provider: provider,
		meter:    provider.Meter("github.com/YipYap-run/YipYap-FOSS/org"),
		endpoint: endpoint,
	}

	// Swap under write lock; shutdown old exporter outside lock.
	m.mu.Lock()
	old, hadOld := m.exporters[orgID]
	m.exporters[orgID] = newExp
	m.mu.Unlock()

	if hadOld {
		if err := old.provider.Shutdown(ctx); err != nil {
			slog.Warn("otel: shutdown error during sync", "org_id", orgID, "error", err)
		}
	}

	slog.Info("otel: started per-org exporter", "org_id", orgID, "endpoint", endpoint)
	return nil
}

// Stop shuts down all per-org exporters.
func (m *OrgExporterManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for orgID, exp := range m.exporters {
		if err := exp.provider.Shutdown(ctx); err != nil {
			slog.Warn("otel: shutdown error", "org_id", orgID, "error", err)
		}
	}
	m.exporters = make(map[string]*orgExporter)
	return nil
}

// StartPeriodicSync runs a background goroutine that syncs every org's
// exporter configuration every 60 seconds. The allOrgIDs function should
// return all org IDs that may have OTEL export configured.
func (m *OrgExporterManager) StartPeriodicSync(ctx context.Context, allOrgIDs func(ctx context.Context) ([]string, error)) {
	go func() {
		t := time.NewTicker(orgExportInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				orgIDs, err := allOrgIDs(ctx)
				if err != nil {
					slog.Warn("otel: failed to list orgs for sync", "error", err)
					continue
				}
				for _, orgID := range orgIDs {
					if err := m.SyncOrg(ctx, orgID); err != nil {
						slog.Warn("otel: sync failed", "org_id", orgID, "error", err)
					}
				}
				// Clean up exporters for orgs not in the synced set.
				synced := make(map[string]struct{}, len(orgIDs))
				for _, id := range orgIDs {
					synced[id] = struct{}{}
				}
				m.mu.Lock()
				for id, exp := range m.exporters {
					if _, ok := synced[id]; !ok {
						_ = exp.provider.Shutdown(ctx)
						delete(m.exporters, id)
						slog.Info("otel: removed stale per-org exporter", "org_id", id)
					}
				}
				m.mu.Unlock()
			}
		}
	}()
}

// parseHeaders parses newline-separated "key=value" pairs into a map.
func parseHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return headers
}
