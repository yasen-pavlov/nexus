// Package netguard provides an HTTP client hardened against server-side
// request forgery (SSRF) for connectors that fetch user-configured URLs.
//
// A regular user can create a connector (e.g. Paperless-ngx) pointing at an
// arbitrary URL, which the server then fetches. Without a guard, a low-
// privileged user could aim it at the host's own loopback services (the
// bundled OpenSearch on :9200, admin endpoints) or the cloud metadata
// endpoint (169.254.169.254) to reach things they otherwise couldn't.
//
// Nexus is a homelab tool, so private LAN ranges (10/8, 172.16/12,
// 192.168/16) are intentionally ALLOWED — that is where a self-hosted
// Paperless or NAS legitimately lives. Only never-legitimate targets are
// blocked: loopback, link-local (incl. metadata), and the unspecified
// address. The check runs in the dialer's Control hook, after DNS resolution
// on the concrete IP about to be dialed, so it also defeats DNS rebinding.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// blocked reports whether an IP is one we refuse to connect to.
func blocked(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// control is the net.Dialer Control hook. It receives the resolved address
// just before the socket connects, so checking it here covers every IP a
// hostname resolves to (defeating DNS rebinding).
func control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("netguard: bad dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("netguard: cannot parse dial IP %q", host)
	}
	if blocked(ip) {
		return fmt.Errorf("netguard: refusing to connect to disallowed address %s", ip)
	}
	return nil
}

// NewDialer returns a net.Dialer whose Control hook rejects loopback/
// link-local/unspecified destinations, applying the same SSRF policy as
// NewClient. Use it for connectors that dial a user-configured host:port over
// a non-HTTP transport (e.g. IMAP-over-TLS), where an http.Client isn't
// applicable. The check runs after DNS resolution on the concrete IP, so it
// defeats DNS rebinding just like the HTTP path.
func NewDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   control,
	}
}

// CheckHost resolves host and returns an error if any resolved IP is a blocked
// (loopback/link-local/unspecified) destination. Use it as a synchronous
// pre-dial guard for non-HTTP connectors that can't route through NewClient;
// pair it with NewDialer so the Control hook still catches a rebind between the
// resolve and the actual connect. host may be a bare hostname or an IP literal.
func CheckHost(ctx context.Context, host string) error {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("netguard: resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if blocked(ip) {
			return fmt.Errorf("netguard: refusing to connect to disallowed address %s (%s)", host, ip)
		}
	}
	return nil
}

// NewClient returns an http.Client whose dialer rejects loopback/link-local/
// unspecified destinations. Use it for any connector that fetches a URL the
// user controls.
func NewClient(timeout time.Duration) *http.Client {
	dialer := NewDialer(10 * time.Second)
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}
