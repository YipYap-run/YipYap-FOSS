package checker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func denyPrivate() bool  { return false }
func allowPrivate() bool { return true }

func TestGuardedClient_RefusesPrivateLiterals(t *testing.T) {
	c := NewGuardedHTTPClient(2*time.Second, denyPrivate)
	for _, u := range []string{
		"http://127.0.0.1:1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.1.2.3/",
		"http://192.168.0.10/",
		"http://[::1]/",
		"http://[::ffff:169.254.169.254]/",
		"http://[::10.0.0.1]/",
		"http://2130706433/", // decimal 127.0.0.1
		"http://0x7f000001/", // hex 127.0.0.1
		"http://localhost/",
		"http://api.localhost/",
	} {
		resp, err := c.Get(u)
		if err == nil {
			_ = resp.Body.Close()
			t.Errorf("GET %s: got a response, want ErrSSRFBlocked", u)
			continue
		}
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Errorf("GET %s: err = %v, want ErrSSRFBlocked", u, err)
		}
	}
}

// TestGuardedClient_RefusesRebind simulates DNS rebinding: the name
// resolved to a public address when the channel was saved, but resolves
// to loopback when the notifier connects.
func TestGuardedClient_RefusesRebind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request reached the loopback server")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	var calls atomic.Int32
	orig := lookupIPAddr
	lookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if calls.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	defer func() { lookupIPAddr = orig }()

	// First lookup is what a create-time check would have seen.
	if _, err := lookupIPAddr(context.Background(), "rebind.example"); err != nil {
		t.Fatal(err)
	}

	c := NewGuardedHTTPClient(2*time.Second, denyPrivate)
	resp, err := c.Get("http://rebind.example:" + port + "/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("rebound name reached loopback")
	}
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, want ErrSSRFBlocked", err)
	}
}

// TestGuardedClient_MixedAnswersBlocked: one private answer is enough to
// refuse the dial, even if a public one comes first.
func TestGuardedClient_MixedAnswersBlocked(t *testing.T) {
	orig := lookupIPAddr
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.5")}}, nil
	}
	defer func() { lookupIPAddr = orig }()

	c := NewGuardedHTTPClient(2*time.Second, denyPrivate)
	resp, err := c.Get("http://mixed.example/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected a block")
	}
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, want ErrSSRFBlocked", err)
	}
}

// TestGuardedClient_DoesNotFollowRedirects: a redirect to the metadata
// address comes back as the 3xx response; no second request is made.
func TestGuardedClient_DoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	// Allow private so the test server on loopback is reachable; the
	// redirect rule applies regardless of the toggle.
	c := NewGuardedHTTPClient(2*time.Second, allowPrivate)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

// TestGuardedClient_IgnoresProxyEnv: a configured proxy would connect to
// the target on our behalf, out of reach of the dial check.
func TestGuardedClient_IgnoresProxyEnv(t *testing.T) {
	c := NewGuardedHTTPClient(time.Second, denyPrivate)
	tr := c.Transport.(*http.Transport)
	if tr.Proxy != nil {
		req := &http.Request{URL: &url.URL{Scheme: "http", Host: "example.com"}}
		if u, _ := tr.Proxy(req); u != nil {
			t.Fatalf("guarded client uses proxy %s", u)
		}
	}
}

func TestGuardedClient_AllowPolicyPermitsLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	resp, err := NewGuardedHTTPClient(2*time.Second, allowPrivate).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestGuardedControl_ChecksSocketAddress(t *testing.T) {
	ctl := guardedControl(denyPrivate)
	for _, a := range []string{"127.0.0.1:80", "[::1]:443", "169.254.169.254:80", "[fd00::1]:80"} {
		if err := ctl("tcp", a, nil); !errors.Is(err, ErrSSRFBlocked) {
			t.Errorf("control(%s) = %v, want ErrSSRFBlocked", a, err)
		}
	}
	if err := ctl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("control(public) = %v", err)
	}
}
