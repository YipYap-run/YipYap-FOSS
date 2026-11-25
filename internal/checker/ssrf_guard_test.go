package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// hitCounter is an httptest server that counts requests.
func hitCounter(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if h != nil {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func port(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	return u.Port()
}

func runHTTPCheck(t *testing.T, rawURL string) *Result {
	t.Helper()
	cfg, _ := json.Marshal(domain.HTTPCheckConfig{Method: "GET", URL: rawURL, ExpectedStatus: 200})
	res, err := (&HTTPChecker{}).Check(context.Background(), cfg)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return res
}

// A name that resolved to a public address when the monitor was saved
// and to 127.0.0.1 when the check runs must not reach the loopback
// listener, and the check must resolve it only once.
func TestHTTPCheck_DNSRebindToLoopbackRefused(t *testing.T) {
	withStrictTargets(t)
	srv, hits := hitCounter(t, nil)

	var calls atomic.Int32
	withResolver(t, func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "rebind.test" {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		if calls.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})

	target := "http://rebind.test:" + port(t, srv) + "/"
	if err := ValidateHTTPTarget(target); err != nil {
		t.Fatalf("save-time validation should pass while the name is public: %v", err)
	}

	res := runHTTPCheck(t, target)
	if res.Status != domain.StatusDown {
		t.Fatalf("status = %s, want down", res.Status)
	}
	if !strings.Contains(res.Error, ErrSSRFBlocked.Error()) {
		t.Errorf("error = %q, want an SSRF block", res.Error)
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback listener was reached %d times", hits.Load())
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("resolver calls = %d, want 2 (one at save, one at dial)", got)
	}
}

// The base dialer only ever sees the vetted IP literal, never the name,
// so it cannot do a second lookup.
func TestHTTPCheck_DialsVettedIP(t *testing.T) {
	withStrictTargets(t)
	srv, hits := hitCounter(t, nil)
	withResolver(t, fakeDNS(map[string][]string{"public.test": {"203.0.113.8"}}))

	var mu sync.Mutex
	var dialed []string
	withBaseDial(t, func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		if addr != "203.0.113.8:"+port(t, srv) {
			return nil, fmt.Errorf("unexpected dial %s", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	})

	res := runHTTPCheck(t, "http://public.test:"+port(t, srv)+"/")
	if res.Status != domain.StatusUp {
		t.Fatalf("status = %s (%s), want up", res.Status, res.Error)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 1 || dialed[0] != "203.0.113.8:"+port(t, srv) {
		t.Errorf("dialed = %v, want only the vetted IP", dialed)
	}
}

// A redirect to a private address is never followed by the checker, and
// the shared transport refuses it even for a client that does follow.
func TestHTTPCheck_RedirectToPrivateRefused(t *testing.T) {
	withStrictTargets(t)
	private, privateHits := hitCounter(t, nil)
	public, _ := hitCounter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/by-name" {
			http.Redirect(w, r, "http://internal.test:"+port(t, private)+"/", http.StatusFound)
			return
		}
		http.Redirect(w, r, private.URL+"/", http.StatusFound)
	})
	withResolver(t, fakeDNS(map[string][]string{
		"public.test":   {"203.0.113.9"},
		"internal.test": {"127.0.0.1"},
	}))
	withBaseDial(t, func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "203.0.113.9:"+port(t, public) {
			return (&net.Dialer{}).DialContext(ctx, network, public.Listener.Addr().String())
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	})

	for _, path := range []string{"/by-ip", "/by-name"} {
		target := "http://public.test:" + port(t, public) + path

		res := runHTTPCheck(t, target)
		if res.StatusCode != http.StatusFound || res.Status != domain.StatusDown {
			t.Errorf("%s: checker result = %d/%s, want an unfollowed 302", path, res.StatusCode, res.Status)
		}

		follow := &http.Client{Transport: sharedTransport}
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		resp, err := follow.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s: following client reached the private target", path)
		}
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Errorf("%s: err = %v, want ErrSSRFBlocked", path, err)
		}
	}
	if privateHits.Load() != 0 {
		t.Fatalf("private listener was reached %d times", privateHits.Load())
	}
}

func TestTCPCheck_DNSRebindRefused(t *testing.T) {
	withStrictTargets(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	withResolver(t, fakeDNS(map[string][]string{"tcp.test": {"127.0.0.1"}}))

	_, p, _ := net.SplitHostPort(ln.Addr().String())
	cfg := json.RawMessage(fmt.Sprintf(`{"host":"tcp.test","port":%s}`, p))
	res, err := (&TCPChecker{}).Check(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != domain.StatusDown || !strings.Contains(res.Error, ErrSSRFBlocked.Error()) {
		t.Fatalf("result = %s %q, want down with an SSRF block", res.Status, res.Error)
	}
}

func TestDNSCheck_PrivateNameserverRefused(t *testing.T) {
	withStrictTargets(t)
	var reached atomic.Bool
	withBaseDial(t, func(ctx context.Context, network, addr string) (net.Conn, error) {
		reached.Store(true)
		return nil, errors.New("must not dial")
	})
	for _, ns := range []string{"127.0.0.1", "169.254.169.254", "ns.internal.test"} {
		withResolver(t, fakeDNS(map[string][]string{"ns.internal.test": {"10.0.0.53"}}))
		cfg, _ := json.Marshal(domain.DNSCheckConfig{Hostname: "example.com", RecordType: "A", Nameserver: ns})
		res, err := (&DNSChecker{}).Check(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != domain.StatusDown || !strings.Contains(res.Error, ErrSSRFBlocked.Error()) {
			t.Errorf("nameserver %s: result = %s %q, want down with an SSRF block", ns, res.Status, res.Error)
		}
	}
	if reached.Load() {
		t.Fatal("a private nameserver was dialed")
	}
}

func TestPingCheck_PrivateAndFlagHostsRefused(t *testing.T) {
	withStrictTargets(t)
	withResolver(t, fakeDNS(map[string][]string{"ping.test": {"10.1.2.3"}}))
	for _, host := range []string{"127.0.0.1", "ping.test", "-c", "nx.invalid"} {
		cfg, _ := json.Marshal(domain.PingCheckConfig{Host: host})
		res, err := (&PingChecker{}).Check(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != domain.StatusDown || res.Error == "" {
			t.Errorf("ping %q: result = %s %q, want refused", host, res.Status, res.Error)
		}
	}
}

func TestMonitorPingTarget_ReturnsVettedIP(t *testing.T) {
	withStrictTargets(t)
	withResolver(t, fakeDNS(map[string][]string{"dual.test": {"2001:db8::1", "198.51.100.4"}}))
	got, err := monitorPingTarget(context.Background(), "dual.test")
	if err != nil || got != "198.51.100.4" {
		t.Fatalf("monitorPingTarget = %q, %v; want the vetted IPv4", got, err)
	}
}

func TestValidate_UnresolvableNames(t *testing.T) {
	withStrictTargets(t)
	origCP := AllowPrivateControlPlaneTargets
	AllowPrivateControlPlaneTargets = false
	defer func() { AllowPrivateControlPlaneTargets = origCP }()

	if err := ValidateTarget("nx.invalid"); !errors.Is(err, ErrUnresolvable) {
		t.Errorf("ValidateTarget = %v, want ErrUnresolvable", err)
	}
	if err := ValidateHTTPTarget("https://nx.invalid/health"); !errors.Is(err, ErrUnresolvable) {
		t.Errorf("ValidateHTTPTarget = %v, want ErrUnresolvable", err)
	}
	// A DNS monitor's query name is looked up, not dialed.
	if err := ValidateDNSQueryName("nx.invalid"); err != nil {
		t.Errorf("ValidateDNSQueryName = %v, want nil", err)
	}
	// Control-plane URLs are vetted again at connect time.
	if err := ValidateControlPlaneHTTPTarget("https://nx.invalid/sink"); err != nil {
		t.Errorf("ValidateControlPlaneHTTPTarget = %v, want nil", err)
	}
	// The resolver error text (which can name the internal resolver) is
	// not passed back to the caller.
	withResolver(t, func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("lookup nx.invalid on 10.96.0.10:53: no such host")
	})
	if err := ValidateTarget("nx.invalid"); err == nil || strings.Contains(err.Error(), "10.96.0.10") {
		t.Errorf("ValidateTarget error leaks resolver detail: %v", err)
	}
}

func TestValidate_NamesResolvingPrivate(t *testing.T) {
	withStrictTargets(t)
	withResolver(t, fakeDNS(map[string][]string{
		"mixed.test": {"203.0.113.1", "10.0.0.1"},
		"meta.test":  {"169.254.169.254"},
		"nat64.test": {"64:ff9b::a9fe:a9fe"},
	}))
	for _, host := range []string{"mixed.test", "meta.test", "nat64.test", "localhost", "db.localhost"} {
		if err := ValidateTarget(host); err == nil {
			t.Errorf("ValidateTarget(%q) = nil, want blocked", host)
		}
		if err := ValidateDNSQueryName(host); err == nil {
			t.Errorf("ValidateDNSQueryName(%q) = nil, want blocked", host)
		}
	}
}

func TestAllowPrivateTargets_PermitsLoopback(t *testing.T) {
	orig := AllowPrivateTargets
	AllowPrivateTargets = true
	defer func() { AllowPrivateTargets = orig }()

	srv, hits := hitCounter(t, nil)
	if err := ValidateHTTPTarget(srv.URL); err != nil {
		t.Fatalf("ValidateHTTPTarget: %v", err)
	}
	res := runHTTPCheck(t, srv.URL)
	if res.Status != domain.StatusUp || hits.Load() != 1 {
		t.Fatalf("status = %s (%s), hits = %d; want up and 1", res.Status, res.Error, hits.Load())
	}

	cfg := json.RawMessage(fmt.Sprintf(`{"host":"127.0.0.1","port":%s}`, port(t, srv)))
	tcp, err := (&TCPChecker{}).Check(context.Background(), cfg)
	if err != nil || tcp.Status != domain.StatusUp {
		t.Fatalf("tcp loopback: %v %+v", err, tcp)
	}
}

// The control-plane dialer resolves once and hands the base dialer the
// vetted IP, like the monitor dialer.
func TestSSRFGuardedDialContext_DialsVettedIP(t *testing.T) {
	origCP := AllowPrivateControlPlaneTargets
	AllowPrivateControlPlaneTargets = false
	defer func() { AllowPrivateControlPlaneTargets = origCP }()

	var calls atomic.Int32
	withResolver(t, func(_ context.Context, host string) ([]net.IPAddr, error) {
		if calls.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.20")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})
	var got string
	dial := SSRFGuardedDialContext(func(_ context.Context, _, addr string) (net.Conn, error) {
		got = addr
		return nil, errors.New("stop")
	})
	_, _ = dial(context.Background(), "tcp", "sink.test:443")
	if got != "203.0.113.20:443" {
		t.Fatalf("base dialed %q, want the vetted IP", got)
	}
	got = ""
	if _, err := dial(context.Background(), "tcp", "sink.test:443"); !errors.Is(err, ErrSSRFBlocked) || got != "" {
		t.Fatalf("rebound name: err = %v, base dialed %q; want ErrSSRFBlocked and no dial", err, got)
	}
}

func TestIsPrivateAddr_ExtendedRanges(t *testing.T) {
	blocked := []string{
		"0.1.2.3",            // 0.0.0.0/8
		"100.100.100.200",    // CGNAT, Alibaba metadata
		"192.0.0.170",        // IETF protocol assignments
		"198.18.0.1",         // benchmarking
		"240.0.0.1",          // reserved
		"255.255.255.255",    // broadcast
		"169.254.169.254",    // metadata
		"::ffff:127.0.0.1",   // IPv4-mapped loopback
		"::ffff:100.64.0.1",  // IPv4-mapped CGNAT
		"fd00:ec2::254",      // AWS IPv6 metadata (ULA)
		"fec0::1",            // site-local
		"100::1",             // discard
		"2001:0:4136:e378::", // Teredo
		"64:ff9b::a00:1",     // NAT64 of 10.0.0.1
		"64:ff9b::7f00:1",    // NAT64 of 127.0.0.1
		"64:ff9b::a9fe:a9fe", // NAT64 of 169.254.169.254
		"64:ff9b:1::1",       // NAT64 local-use
		"2002:a00:1::1",      // 6to4 of 10.0.0.1
		"2002:7f00:1::1",     // 6to4 of 127.0.0.1
	}
	for _, s := range blocked {
		if ok, _ := IsPrivateAddr(net.ParseIP(s)); !ok {
			t.Errorf("IsPrivateAddr(%s) = false, want blocked", s)
		}
	}
	allowed := []string{
		"8.8.8.8",
		"203.0.113.1",
		"64:ff9b::808:808", // NAT64 of 8.8.8.8
		"2002:808:808::1",  // 6to4 of 8.8.8.8
		"2606:4700::1111",
	}
	for _, s := range allowed {
		if ok, why := IsPrivateAddr(net.ParseIP(s)); ok {
			t.Errorf("IsPrivateAddr(%s) = blocked (%s), want allowed", s, why)
		}
	}
}

func TestFilterFamily(t *testing.T) {
	ips := []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("2001:db8::1")}
	if got := filterFamily("tcp4", ips); len(got) != 1 || got[0].To4() == nil {
		t.Errorf("tcp4 = %v", got)
	}
	if got := filterFamily("tcp6", ips); len(got) != 1 || got[0].To4() != nil {
		t.Errorf("tcp6 = %v", got)
	}
	if got := filterFamily("tcp", ips); len(got) != 2 {
		t.Errorf("tcp = %v", got)
	}
}

func TestConfigureTargets(t *testing.T) {
	origM, origCP := AllowPrivateTargets, AllowPrivateControlPlaneTargets
	defer func() { AllowPrivateTargets, AllowPrivateControlPlaneTargets = origM, origCP }()
	ConfigureTargets(true, false)
	if !AllowPrivateTargets || AllowPrivateControlPlaneTargets {
		t.Fatal("ConfigureTargets(true, false) did not apply")
	}
	ConfigureTargets(false, true)
	if AllowPrivateTargets || !AllowPrivateControlPlaneTargets {
		t.Fatal("ConfigureTargets(false, true) did not apply")
	}
}

// TestGuardedDial_ErrorReadsOnce checks the refusal text that lands in check
// history: it names the host and the address once, and still matches both
// ErrSSRFBlocked and the private-target class.
func TestGuardedDial_ErrorReadsOnce(t *testing.T) {
	withStrictTargets(t)
	withResolver(t, fakeDNS(map[string][]string{"rebind.test": {"127.0.0.1"}}))

	_, err := monitorDialContext(context.Background(), "tcp", "rebind.test:80")
	if err == nil {
		t.Fatal("dial succeeded, want a refusal")
	}
	if !errors.Is(err, ErrSSRFBlocked) || !IsPrivateTargetError(err) {
		t.Errorf("err = %v, want ErrSSRFBlocked and a private-target error", err)
	}
	if n := strings.Count(err.Error(), "rebind.test"); n != 1 {
		t.Errorf("err = %q names the host %d times, want 1", err, n)
	}
	if !strings.Contains(err.Error(), "resolves to 127.0.0.1") {
		t.Errorf("err = %q, want the resolved address", err)
	}
}
