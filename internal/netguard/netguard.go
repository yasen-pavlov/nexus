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

// NewClient returns an http.Client whose dialer rejects loopback/link-local/
// unspecified destinations. Use it for any connector that fetches a URL the
// user controls.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   control,
	}
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
