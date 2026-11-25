package checker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// PingChecker performs ICMP echo checks. It sends a real echo request and
// waits for the matching reply.
//
// The release images run as an unprivileged user (core-19), so it first uses
// an unprivileged ICMP datagram socket. Linux allows that when the process
// group is inside net.ipv4.ping_group_range (Docker and containerd 2.x set the
// full range by default); macOS always allows it. It then tries a raw socket
// (root or CAP_NET_RAW), and last the system ping command, which does not
// exist in the scratch images.
type PingChecker struct{}

const icmpProtoV4, icmpProtoV6 = 1, 58

// pingSocket is one way to open an ICMP socket.
type pingSocket struct {
	network string // icmp.ListenPacket network
	addr    string // local listen address
	udp     bool   // datagram socket: the kernel owns the echo ID
}

var (
	pingSocketsV4 = []pingSocket{{"udp4", "0.0.0.0", true}, {"ip4:icmp", "0.0.0.0", false}}
	pingSocketsV6 = []pingSocket{{"udp6", "::", true}, {"ip6:ipv6-icmp", "::", false}}
)

// errNoICMPSocket means no ICMP socket could be opened at all, as opposed to
// the target not answering.
var errNoICMPSocket = errors.New("no ICMP socket available")

// lookPath is swapped in tests to simulate an image with no ping binary.
var lookPath = exec.LookPath

func (c *PingChecker) Check(ctx context.Context, config json.RawMessage) (*Result, error) {
	var cfg domain.PingCheckConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("ping checker: unmarshal config: %w", err)
	}
	if cfg.Host == "" {
		return &Result{Status: domain.StatusDown, Error: "ping: host is required"}, nil
	}

	// Resolve and vet once, then ping the vetted address so neither the
	// socket path nor the system ping command does a second lookup (mon-4).
	target, err := monitorPingTarget(ctx, cfg.Host)
	if err != nil {
		return &Result{Status: domain.StatusDown, Error: err.Error()}, nil
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}

	result, sockErr := c.echo(ctx, target, deadline)
	if sockErr == nil {
		return result, nil
	}

	// No ICMP socket could be opened. Use the system ping command if there
	// is one (bare-metal installs running as a normal user).
	if _, err := lookPath("ping"); err != nil {
		return &Result{
			Status: domain.StatusDown,
			Error: fmt.Sprintf("ping unavailable: %v; allow unprivileged ICMP "+
				"(sysctl net.ipv4.ping_group_range) or grant CAP_NET_RAW", sockErr),
		}, nil
	}
	return c.execPing(ctx, target, deadline)
}

// echo resolves host, sends one ICMP echo request and waits for the reply
// until deadline. It returns a non-nil error only when no ICMP socket could be
// opened; every other failure is a Down result.
func (c *PingChecker) echo(ctx context.Context, host string, deadline time.Time) (*Result, error) {
	ip, err := resolvePingTarget(ctx, host)
	if err != nil {
		return &Result{Status: domain.StatusDown, Error: fmt.Sprintf("resolve %s: %v", host, err)}, nil
	}

	sockets, proto := pingSocketsV4, icmpProtoV4
	var reqType, replyType icmp.Type = ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	if ip.To4() == nil {
		sockets, proto = pingSocketsV6, icmpProtoV6
		reqType, replyType = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
	}

	var conn *icmp.PacketConn
	var sock pingSocket
	var openErrs []error
	for _, s := range sockets {
		conn, err = icmp.ListenPacket(s.network, s.addr)
		if err == nil {
			sock = s
			break
		}
		openErrs = append(openErrs, fmt.Errorf("%s: %w", s.network, err))
	}
	if conn == nil {
		return nil, fmt.Errorf("%w: %w", errNoICMPSocket, errors.Join(openErrs...))
	}
	defer func() { _ = conn.Close() }()

	// Random ID, sequence and payload so a reply to another checker on the
	// same host is never taken for ours. A datagram socket rewrites the ID,
	// so matching relies on the sequence and payload there.
	var nonce [20]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("ping: random: %w", err)
	}
	id := int(binary.BigEndian.Uint16(nonce[0:2]))
	seq := int(binary.BigEndian.Uint16(nonce[2:4]))
	payload := nonce[4:]

	msg := icmp.Message{Type: reqType, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return nil, fmt.Errorf("ping: marshal: %w", err)
	}

	var dst net.Addr = &net.IPAddr{IP: ip}
	if sock.udp {
		dst = &net.UDPAddr{IP: ip}
	}

	if err := conn.SetDeadline(deadline); err != nil {
		return &Result{Status: domain.StatusDown, Error: fmt.Sprintf("ping: %v", err)}, nil
	}
	// Close the socket if the context is cancelled before the deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	start := time.Now()
	if _, err := conn.WriteTo(wb, dst); err != nil {
		return &Result{Status: domain.StatusDown, Error: fmt.Sprintf("ping %s: %v", ip, err)}, nil
	}

	rb := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return &Result{
					Status:    domain.StatusDown,
					LatencyMS: int(time.Since(start).Milliseconds()),
					Error:     fmt.Sprintf("ping %s: no echo reply before timeout", ip),
				}, nil
			}
			return &Result{Status: domain.StatusDown, Error: fmt.Sprintf("ping %s: %v", ip, err)}, nil
		}
		if !peerIP(peer).Equal(ip) {
			continue
		}
		rm, err := icmp.ParseMessage(proto, rb[:n])
		if err != nil || rm.Type != replyType {
			continue
		}
		reply, ok := rm.Body.(*icmp.Echo)
		if !ok || reply.Seq != seq || !bytes.Equal(reply.Data, payload) {
			continue
		}
		if !sock.udp && reply.ID != id {
			continue
		}
		return &Result{
			Status:    domain.StatusUp,
			LatencyMS: int(time.Since(start).Milliseconds()),
		}, nil
	}
}

// resolvePingTarget returns the first IPv4 address for host, or the first
// IPv6 address when it has no IPv4 one.
func resolvePingTarget(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			return v4, nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0].IP, nil
	}
	return nil, fmt.Errorf("no addresses")
}

func peerIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	return nil
}

func (c *PingChecker) execPing(ctx context.Context, host string, deadline time.Time) (*Result, error) {
	timeoutSec := int(time.Until(deadline).Seconds())
	if timeoutSec < 1 {
		timeoutSec = 1
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(timeoutSec*1000), host)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(timeoutSec), host)
	}

	start := time.Now()
	err := cmd.Run()
	latency := time.Since(start)

	result := &Result{
		LatencyMS: int(latency.Milliseconds()),
	}

	if err != nil {
		result.Status = domain.StatusDown
		result.Error = fmt.Sprintf("ping failed: %v", err)
		return result, nil
	}

	result.Status = domain.StatusUp
	return result, nil
}
