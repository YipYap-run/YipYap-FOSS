package telemetry

import (
	"os"
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

func TestResolveInstanceID(t *testing.T) {
	if got := resolveInstanceID("replica-7"); got != "replica-7" {
		t.Fatalf("explicit override: got %q, want replica-7", got)
	}
	// With no override the id must still be non-empty, so replicas never
	// share a blank service.instance.id.
	if got := resolveInstanceID(""); got == "" {
		t.Fatal("empty override should still produce a non-empty instance id")
	}
	// The hostname is preferred when available.
	if h, err := os.Hostname(); err == nil && h != "" {
		if got := resolveInstanceID(""); got != h {
			t.Fatalf("expected hostname %q, got %q", h, got)
		}
	}
}

func TestNewResourceCarriesInstanceID(t *testing.T) {
	res, err := newResource(SetupOptions{ServiceName: "yipyap-checker", InstanceID: "replica-7"})
	if err != nil {
		t.Fatalf("newResource: %v", err)
	}

	var name, instance string
	for _, kv := range res.Attributes() {
		switch kv.Key {
		case semconv.ServiceNameKey:
			name = kv.Value.AsString()
		case semconv.ServiceInstanceIDKey:
			instance = kv.Value.AsString()
		}
	}

	if name != "yipyap-checker" {
		t.Errorf("service.name = %q, want yipyap-checker", name)
	}
	if instance != "replica-7" {
		t.Errorf("service.instance.id = %q, want replica-7", instance)
	}
}
