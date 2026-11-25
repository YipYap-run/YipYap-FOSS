package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

func TestParseOTLPEndpoint(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"collector.example.com:4317", true},
		{"[2001:db8::1]:4317", true},
		{"collector.example.com", false},
		{"https://collector.example.com:4317", false},
		{"collector.example.com:4317/v1/metrics", false},
		{"collector.example.com:0", false},
		{"collector.example.com:70000", false},
		{"collector.example.com:abc", false},
		{":4317", false},
		{"user@collector.example.com:4317", false},
		{"", false},
	} {
		_, _, err := telemetry.ParseOTLPEndpoint(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseOTLPEndpoint(%q) err=%v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

// Without the insecure opt-in the exporter must use TLS, so a plaintext
// collector never receives metrics (and auth headers never go out in clear
// text).
func TestOrgExporterDefaultsToTLS(t *testing.T) {
	ctx := context.Background()
	endpoint, fake := startFakeOTLP(t)

	settings := newMockSettings()
	manager := telemetry.NewOrgExporterManager(settings, func(s string) (string, error) { return s, nil })
	settings.set("tls-org", telemetry.SettingOTELEndpoint, endpoint)
	settings.set("tls-org", telemetry.SettingOTELHeaders, "x-api-key=secret")

	if err := manager.SyncOrg(ctx, "tls-org"); err != nil {
		t.Fatalf("SyncOrg: %v", err)
	}
	counter, err := manager.GetMeter("tls-org").Int64Counter("tls.default.counter")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)

	stopCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = manager.Stop(stopCtx)

	if names := collectMetricNames(fake, 500*time.Millisecond); len(names) != 0 {
		t.Fatalf("plaintext collector received metrics over a default (TLS) exporter: %v", names)
	}
}

func TestOrgExporterRejectsGuardedHost(t *testing.T) {
	ctx := context.Background()
	settings := newMockSettings()
	blocked := errors.New("blocked")
	manager := telemetry.NewOrgExporterManager(settings, nil).WithGuards(
		func(host string) error {
			if host == "10.0.0.5" {
				return blocked
			}
			return nil
		}, nil)
	t.Cleanup(func() { _ = manager.Stop(ctx) })

	settings.set("org", telemetry.SettingOTELEndpoint, "10.0.0.5:4317")
	if err := manager.SyncOrg(ctx, "org"); !errors.Is(err, blocked) {
		t.Fatalf("SyncOrg err = %v, want the guard error", err)
	}
	if manager.GetMeter("org") != nil {
		t.Fatal("exporter was created for a blocked host")
	}

	settings.set("org", telemetry.SettingOTELEndpoint, "no-port")
	if err := manager.SyncOrg(ctx, "org"); err == nil {
		t.Fatal("SyncOrg accepted an endpoint without a port")
	}
}

// Changing headers or the TLS mode must rebuild the exporter; before the fix
// only an endpoint change did, so a rotated API key never took effect.
func TestOrgExporterRebuildsOnHeaderOrTLSChange(t *testing.T) {
	ctx := context.Background()
	settings := newMockSettings()
	manager := telemetry.NewOrgExporterManager(settings, func(s string) (string, error) { return s, nil })
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_ = manager.Stop(sctx)
	})

	settings.set("org", telemetry.SettingOTELEndpoint, "collector.example.com:4317")
	settings.set("org", telemetry.SettingOTELHeaders, "x-api-key=old")
	if err := manager.SyncOrg(ctx, "org"); err != nil {
		t.Fatal(err)
	}
	first := manager.GetMeter("org")

	if err := manager.SyncOrg(ctx, "org"); err != nil {
		t.Fatal(err)
	}
	if manager.GetMeter("org") != first {
		t.Fatal("unchanged config rebuilt the exporter")
	}

	settings.set("org", telemetry.SettingOTELHeaders, "x-api-key=new")
	if err := manager.SyncOrg(ctx, "org"); err != nil {
		t.Fatal(err)
	}
	second := manager.GetMeter("org")
	if second == first {
		t.Fatal("header change did not rebuild the exporter")
	}

	settings.set("org", telemetry.SettingOTELInsecure, "true")
	if err := manager.SyncOrg(ctx, "org"); err != nil {
		t.Fatal(err)
	}
	if manager.GetMeter("org") == second {
		t.Fatal("TLS mode change did not rebuild the exporter")
	}
}
