package telemetry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

	// validateHost and dial are the SSRF guards. telemetry cannot import
	// internal/checker (checker imports telemetry), so the wiring code
	// injects them with WithGuards. Both nil means no guard, which only
	// tests should rely on.
	validateHost func(host string) error
	dial         func(ctx context.Context, addr string) (net.Conn, error)
}

type orgExporter struct {
	provider *sdkmetric.MeterProvider
	meter    metric.Meter
	// fingerprint covers endpoint, TLS mode and headers so a change to any
	// of them rebuilds the exporter, not only an endpoint change.
	fingerprint string
}

// Org settings keys for per-org OTLP export.
const (
	SettingOTELEndpoint = "otel_endpoint"
	SettingOTELHeaders  = "otel_headers"
	// SettingOTELInsecure holds "true" when the org explicitly opted out of
	// TLS. Absent or any other value means TLS.
	SettingOTELInsecure = "otel_insecure"
)

// ParseOTLPEndpoint validates a gRPC OTLP endpoint in host:port form and
// returns the host and port. It rejects schemes, paths, and ports outside
// 1-65535. It does not check the host against SSRF rules; callers do that
// with the checker package.
func ParseOTLPEndpoint(raw string) (host, port string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("endpoint is required")
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#@ ") {
		return "", "", errors.New("endpoint must be in host:port format, without a scheme or path")
	}
	host, port, err = net.SplitHostPort(raw)
	if err != nil || host == "" {
		return "", "", errors.New("endpoint must be in host:port format")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", "", errors.New("endpoint port must be between 1 and 65535")
	}
	return host, port, nil
}

// WithGuards installs the SSRF guards: validateHost runs on the configured
// host before an exporter is built, and dial replaces the gRPC dialer so the
// resolved address is checked again at connect time (DNS rebinding).
func (m *OrgExporterManager) WithGuards(validateHost func(string) error, dial func(ctx context.Context, addr string) (net.Conn, error)) *OrgExporterManager {
	m.validateHost = validateHost
	m.dial = dial
	return m
}

// TransportCredentials returns TLS credentials (TLS 1.2 minimum) unless
// insecure is true. Shared by the exporter and the settings Test button so
// both connect the same way.
func TransportCredentials(insecure bool) credentials.TransportCredentials {
	if insecure {
		return nil
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
}

func configFingerprint(endpoint string, insecure bool, headers string) string {
	h := sha256.Sum256([]byte(endpoint + "\x00" + strconv.FormatBool(insecure) + "\x00" + headers))
	return hex.EncodeToString(h[:])
}

const orgExportInterval = 60 * time.Second

// replacedShutdownTimeout bounds the final flush of an exporter that a
// config change replaced.
const replacedShutdownTimeout = 10 * time.Second

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

	endpoint := settings[SettingOTELEndpoint]
	insecureTransport := settings[SettingOTELInsecure] == "true"

	// Check current state under read lock.
	m.mu.RLock()
	existing, exists := m.exporters[orgID]
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

	host, _, err := ParseOTLPEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("otel exporter for org %s: %w", orgID, err)
	}
	if m.validateHost != nil {
		if err := m.validateHost(host); err != nil {
			return fmt.Errorf("otel exporter for org %s: %w", orgID, err)
		}
	}

	// Decrypt headers if present.
	headers := ""
	if raw, ok := settings[SettingOTELHeaders]; ok && raw != "" {
		if m.decrypt != nil {
			decrypted, err := m.decrypt(raw)
			if err != nil {
				slog.Warn("otel: failed to decrypt headers", "org_id", orgID, "error", err)
			} else {
				headers = decrypted
			}
		}
	}

	// Nothing changed -- keep the running exporter.
	fp := configFingerprint(endpoint, insecureTransport, headers)
	if exists && existing.fingerprint == fp {
		return nil
	}

	// Create OTLP exporter with org-specific endpoint OUTSIDE the lock.
	// TLS is the default; plaintext only when the org opted in.
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(endpoint)}
	if insecureTransport {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(TransportCredentials(false)))
	}
	if m.dial != nil {
		opts = append(opts, otlpmetricgrpc.WithDialOption(grpc.WithContextDialer(m.dial)))
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
		provider:    provider,
		meter:       provider.Meter("github.com/YipYap-run/YipYap-FOSS/org"),
		fingerprint: fp,
	}

	// Swap under write lock; shutdown old exporter outside lock.
	m.mu.Lock()
	old, hadOld := m.exporters[orgID]
	m.exporters[orgID] = newExp
	m.mu.Unlock()

	if hadOld {
		// Flush and close the replaced exporter in the background with a
		// bounded timeout. A slow or unreachable old collector must not
		// stall the sync of every other org.
		go func(p *sdkmetric.MeterProvider) {
			sctx, cancel := context.WithTimeout(context.Background(), replacedShutdownTimeout)
			defer cancel()
			if err := p.Shutdown(sctx); err != nil {
				slog.Warn("otel: shutdown error during sync", "org_id", orgID, "error", err)
			}
		}(old.provider)
	}

	slog.Info("otel: started per-org exporter", "org_id", orgID, "endpoint", endpoint, "tls", !insecureTransport)
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
