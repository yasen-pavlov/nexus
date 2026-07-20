package netguard

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestBlocked(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},       // loopback — Nexus's own services
		{"::1", true},             // loopback v6
		{"169.254.169.254", true}, // cloud metadata (link-local)
		{"169.254.1.1", true},     // link-local
		{"0.0.0.0", true},         // unspecified
		{"::", true},              // unspecified v6
		{"192.168.1.10", false},   // private LAN — legitimate NAS/Paperless
		{"10.0.0.5", false},       // private LAN
		{"172.16.4.2", false},     // private LAN
		{"8.8.8.8", false},        // public
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", tt.ip)
		}
		if got := blocked(ip); got != tt.want {
			t.Errorf("blocked(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestControl_RejectsLoopback(t *testing.T) {
	if err := control("tcp", "127.0.0.1:9200", nil); err == nil {
		t.Error("expected control to reject loopback dial")
	}
	if err := control("tcp", "192.168.1.5:8000", nil); err != nil {
		t.Errorf("expected control to allow private LAN dial, got %v", err)
	}
}

func TestControl_ErrorPaths(t *testing.T) {
	// Address without a port → SplitHostPort fails.
	if err := control("tcp", "no-port", nil); err == nil {
		t.Error("expected error for address without host:port")
	}
	// Host:port whose host isn't a parseable IP (control runs post-resolution,
	// so a non-IP host is unexpected) → parse error.
	if err := control("tcp", "example.com:443", nil); err == nil {
		t.Error("expected error for non-IP host")
	}
}

func TestNewClient_NotNil(t *testing.T) {
	if NewClient(0) == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestNewDialer_HasControlHook(t *testing.T) {
	d := NewDialer(0)
	if d == nil {
		t.Fatal("NewDialer returned nil")
	}
	if d.Control == nil {
		t.Fatal("NewDialer must install the SSRF Control hook")
	}
	// The hook enforces the same policy as control().
	if err := d.Control("tcp", "127.0.0.1:993", nil); err == nil {
		t.Error("dialer Control should reject loopback")
	}
}

func TestCheckHost(t *testing.T) {
	ctx := context.Background()
	// IP literals resolve without touching the network, so these are hermetic.
	blockedHosts := []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0"}
	for _, h := range blockedHosts {
		if err := CheckHost(ctx, h); err == nil {
			t.Errorf("CheckHost(%q) = nil, want rejection", h)
		}
	}
	allowedHosts := []string{"192.168.1.10", "10.0.0.5", "8.8.8.8"}
	for _, h := range allowedHosts {
		if err := CheckHost(ctx, h); err != nil {
			t.Errorf("CheckHost(%q) = %v, want allow", h, err)
		}
	}
}

func TestCheckHost_ResolveError(t *testing.T) {
	// An unresolvable host surfaces a resolve error rather than silently
	// allowing the dial.
	err := CheckHost(context.Background(), "no-such-host.invalid")
	if err == nil || !strings.Contains(err.Error(), "resolve") {
		t.Errorf("CheckHost(bad) = %v, want a resolve error", err)
	}
}
