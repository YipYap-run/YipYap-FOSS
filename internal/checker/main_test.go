package checker

import (
	"context"
	"net"
	"os"
	"testing"
)

// TestMain makes the package tests hermetic and lets the existing check
// tests reach their httptest servers on 127.0.0.1. SSRF tests turn the
// guard back on explicitly with withStrictTargets.
func TestMain(m *testing.M) {
	AllowPrivateTargets = true
	lookupIPAddr = fakeDNS(map[string][]string{
		"example.com": {"93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"},
	})
	os.Exit(m.Run())
}

// fakeDNS returns a resolver that answers from records and reports every
// other name as not found.
func fakeDNS(records map[string][]string) func(context.Context, string) ([]net.IPAddr, error) {
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		ips, ok := records[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		out := make([]net.IPAddr, 0, len(ips))
		for _, s := range ips {
			out = append(out, net.IPAddr{IP: net.ParseIP(s)})
		}
		return out, nil
	}
}

// withStrictTargets turns monitor SSRF protection on for one test.
func withStrictTargets(t *testing.T) {
	t.Helper()
	orig := AllowPrivateTargets
	AllowPrivateTargets = false
	t.Cleanup(func() { AllowPrivateTargets = orig })
}

// withResolver swaps the resolver for one test.
func withResolver(t *testing.T, fn func(context.Context, string) ([]net.IPAddr, error)) {
	t.Helper()
	orig := lookupIPAddr
	lookupIPAddr = fn
	t.Cleanup(func() { lookupIPAddr = orig })
}

// withBaseDial swaps the post-vetting connect for one test.
func withBaseDial(t *testing.T, fn dialFunc) {
	t.Helper()
	orig := monitorBaseDial
	monitorBaseDial = fn
	t.Cleanup(func() { monitorBaseDial = orig })
}
