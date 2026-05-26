//go:build knative

// Package knative contains end-to-end integration tests that require a
// running kind cluster with Knative Eventing installed. See README.md for
// setup. These tests are double-gated: the `knative` build tag must be set
// AND the KNATIVE=1 env var must be present at runtime, otherwise the test
// skips.
package knative

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
)

const (
	// clusterName mirrors the default in setup.sh and is the target
	// kubecontext used by the `kubectl` invocations in this test.
	clusterName = "yipyap-knative"

	// kubectlContext is the kind-provisioned context pointing at the test
	// cluster.
	kubectlContext = "kind-" + clusterName

	// brokerIngressPort is the random-ish host port we tunnel Broker
	// ingress to via `kubectl port-forward`.
	brokerIngressPort = "18080"
)

// requireKnative skips the test unless KNATIVE=1 is set and basic cluster
// preconditions are met (kubectl on PATH, cluster context exists).
func requireKnative(t *testing.T) {
	t.Helper()
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 not set; skipping Knative integration test")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not on PATH; skipping Knative integration test")
	}
	// Confirm the kind cluster context exists, if `setup.sh up` hasn't
	// been run we'd otherwise produce cryptic failures.
	cmd := exec.Command("kubectl", "config", "get-contexts", "-o", "name")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("kubectl config get-contexts failed: %v", err)
	}
	if !strings.Contains(string(out), kubectlContext) {
		t.Skipf("kube context %q not found; run ./test/integration/knative/setup.sh up", kubectlContext)
	}
}

// kubectl runs `kubectl --context <ctx> <args...>` and returns combined output.
func kubectl(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"--context", kubectlContext}, args...)
	cmd := exec.CommandContext(ctx, "kubectl", full...)
	return cmd.CombinedOutput()
}

// mustKubectl fails the test on non-zero exit, attaching combined output.
func mustKubectl(t *testing.T, ctx context.Context, args ...string) []byte {
	t.Helper()
	out, err := kubectl(ctx, args...)
	if err != nil {
		t.Fatalf("kubectl %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// fixturePath resolves a file under ./fixtures/ relative to this test file.
func fixturePath(name string) string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "fixtures", name)
}

// waitFor polls f every `interval` until it returns nil or timeout expires.
func waitFor(t *testing.T, timeout, interval time.Duration, desc string, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := f(); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}
	t.Fatalf("timed out waiting for %s: %v", desc, lastErr)
}

// TestOutbound_CloudEventReachesBroker drives the cloudevent_http provider
// against a real Knative Broker and asserts the event lands at
// event-display via a Trigger filter.
func TestOutbound_CloudEventReachesBroker(t *testing.T) {
	requireKnative(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Use a unique namespace per test run so parallel runs don't collide
	// and teardown is a single `kubectl delete ns`.
	ns := fmt.Sprintf("yipyap-out-%d", time.Now().UnixNano())
	mustKubectl(t, ctx, "create", "namespace", ns)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer ccancel()
		_, _ = kubectl(cctx, "delete", "namespace", ns, "--wait=false")
	})

	// 1) event-display sink.
	mustKubectl(t, ctx, "apply", "-n", ns, "-f", fixturePath("event-display.yaml"))

	// 2) Broker (re-applies broker-default configmap too; that's fine,
	//    apply is a no-op if it already matches).
	mustKubectl(t, ctx, "apply", "-n", ns, "-f", fixturePath("broker.yaml"))

	// 3) Trigger targeting event-display and filtering on ce.type.
	mustKubectl(t, ctx, "apply", "-n", ns, "-f", fixturePath("trigger.yaml"))

	// Wait for Broker -> Addressable (needs an address.url we can POST to).
	waitFor(t, 3*time.Minute, 2*time.Second, "broker ready",
		func() error {
			out, err := kubectl(ctx, "get", "broker", "default", "-n", ns,
				"-o", "jsonpath={.status.address.url}")
			if err != nil {
				return err
			}
			if len(bytes.TrimSpace(out)) == 0 {
				return fmt.Errorf("broker address.url empty")
			}
			return nil
		})

	// Wait for Trigger ready.
	waitFor(t, 2*time.Minute, 2*time.Second, "trigger ready",
		func() error {
			out, err := kubectl(ctx, "get", "trigger", "yipyap-alert-fired", "-n", ns,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(out)) != "True" {
				return fmt.Errorf("trigger not Ready: %s", out)
			}
			return nil
		})

	// Wait for event-display Deployment rollout.
	mustKubectl(t, ctx, "rollout", "status", "deployment/event-display", "-n", ns, "--timeout=120s")

	// Resolve the Broker's cluster-internal ingress service
	// (knative-eventing/broker-ingress). Port-forward into the test host.
	pfCtx, pfCancel := context.WithCancel(ctx)
	t.Cleanup(pfCancel)
	pfCmd := exec.CommandContext(pfCtx, "kubectl",
		"--context", kubectlContext,
		"port-forward", "-n", "knative-eventing", "svc/broker-ingress",
		brokerIngressPort+":80",
	)
	if err := pfCmd.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	t.Cleanup(func() { _ = pfCmd.Process.Kill(); _ = pfCmd.Wait() })

	// Wait for port-forward to accept connections.
	waitFor(t, 30*time.Second, 500*time.Millisecond, "port-forward listening", func() error {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+brokerIngressPort, 1*time.Second)
		if err != nil {
			return err
		}
		_ = c.Close()
		return nil
	})

	// Build the CloudEvent we want to deliver.
	ev, err := cloudevents.NewAlertFiredV1(
		"https://console.yipyap.run/orgs/kind-test",
		types.AlertFiredV1Data{
			AlertID:   "alert-kind-1",
			MonitorID: "mon-kind-1",
			Severity:  "critical",
			Reason:    "kind integration",
			FiredAt:   time.Now().UTC(),
		})
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	msg, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	// Compose sink URL targeting the Broker's ingress path for our
	// namespace/name. broker-ingress expects path `/{namespace}/{name}`.
	sinkURL := fmt.Sprintf("http://127.0.0.1:%s/%s/default", brokerIngressPort, ns)

	cfg := providers.CloudEventHTTPConfig{
		SinkURL: sinkURL,
		Mode:    "binary",
	}
	cfgJSON, _ := json.Marshal(cfg)

	job := domain.NotificationJob{
		ID:           "kind-job-1",
		AlertID:      "alert-kind-1",
		OrgID:        "org-kind",
		MonitorName:  "kind-test",
		Severity:     "critical",
		Channel:      "cloudevent_http",
		Message:      string(msg),
		TargetConfig: encodePlaintextConfig(cfgJSON),
	}

	p := providers.NewCloudEventHTTP(nil)
	id, err := p.Send(ctx, job)
	if err != nil {
		t.Fatalf("provider.Send: %v", err)
	}
	if id != ev.ID() {
		t.Fatalf("expected ce-id %q returned, got %q", ev.ID(), id)
	}

	// Poll event-display pod logs for up to 30s looking for our ce-id.
	waitFor(t, 30*time.Second, 1*time.Second, "event-display to log our event",
		func() error {
			out, err := kubectl(ctx, "logs", "deployment/event-display", "-n", ns)
			if err != nil {
				return err
			}
			if !strings.Contains(string(out), ev.ID()) {
				return fmt.Errorf("ce-id %q not yet in logs", ev.ID())
			}
			if !strings.Contains(string(out), ev.Type()) {
				return fmt.Errorf("ce-type %q not yet in logs", ev.Type())
			}
			return nil
		})
}

// encodePlaintextConfig wraps `cfgJSON` in the plaintext (0x00-prefix)
// version envelope used by providers.resolveConfig and base64-encodes the
// result. Matches helper in providers/cloudevent_http_test.go.
func encodePlaintextConfig(jsonBytes []byte) string {
	buf := make([]byte, 0, len(jsonBytes)+1)
	buf = append(buf, 0x00)
	buf = append(buf, jsonBytes...)
	return base64.StdEncoding.EncodeToString(buf)
}
