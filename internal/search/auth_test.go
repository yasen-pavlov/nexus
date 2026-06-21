package search

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// --- buildClientConfig: pure wiring assertions (no network) ------------------

func TestBuildClientConfig_NoAuth(t *testing.T) {
	cfg, err := buildClientConfig("http://localhost:9200", AuthConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Username != "" || cfg.Password != "" {
		t.Errorf("expected no credentials, got user=%q", cfg.Username)
	}
	if cfg.Transport != nil {
		t.Errorf("plain config should not build a custom transport, got %T", cfg.Transport)
	}
	if len(cfg.Addresses) != 1 || cfg.Addresses[0] != "http://localhost:9200" {
		t.Errorf("addresses not set: %v", cfg.Addresses)
	}
}

func TestBuildClientConfig_BasicAuthOnly(t *testing.T) {
	cfg, err := buildClientConfig("http://localhost:9200", AuthConfig{Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Username != "admin" || cfg.Password != "secret" {
		t.Errorf("credentials not set: user=%q pass=%q", cfg.Username, cfg.Password)
	}
	if cfg.Transport != nil {
		t.Errorf("basic auth alone should not build a TLS transport, got %T", cfg.Transport)
	}
}

func TestBuildClientConfig_SkipVerify(t *testing.T) {
	cfg, err := buildClientConfig("https://opensearch:9200", AuthConfig{Username: "admin", Password: "secret", SkipVerify: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := cfg.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", cfg.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true")
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS1.2, got %d", tr.TLSClientConfig.MinVersion)
	}
	if tr.TLSClientConfig.RootCAs != nil {
		t.Error("skip-verify should not populate RootCAs")
	}
}

func TestBuildClientConfig_CAFileMissing(t *testing.T) {
	_, err := buildClientConfig("https://opensearch:9200", AuthConfig{CAFile: filepath.Join(t.TempDir(), "nope.pem")})
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestBuildClientConfig_CAFileInvalid(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := buildClientConfig("https://opensearch:9200", AuthConfig{CAFile: bad})
	if err == nil {
		t.Fatal("expected error for CA file with no certificates")
	}
}

// --- New(): end-to-end auth + TLS against a real self-signed TLS server ------
//
// httptest.NewTLSServer gives us a genuine TLS endpoint with a self-signed cert
// (valid for 127.0.0.1), so we can exercise the full New -> buildClientConfig ->
// opensearch client -> TLS handshake -> basic-auth path without a container.

const (
	testOSUser = "admin"
	testOSPass = "s3cr3t!"
)

// authInfoServer responds with a minimal OpenSearch Info document, but only when
// the request carries the expected basic-auth credentials (otherwise 401).
func authInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	const infoBody = `{"name":"n","cluster_name":"c","cluster_uuid":"u",` +
		`"version":{"number":"2.11.0","distribution":"opensearch","build_type":"tar",` +
		`"build_hash":"x","build_date":"2024-01-01T00:00:00Z","build_snapshot":false,` +
		`"lucene_version":"9.7.0","minimum_wire_compatibility_version":"7.10.0",` +
		`"minimum_index_compatibility_version":"7.0.0"},"tagline":"The OpenSearch Project"}`

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testOSUser || pass != testOSPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="opensearch"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(infoBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNew_SkipVerifyWithCredentials(t *testing.T) {
	srv := authInfoServer(t)
	_, err := New(context.Background(), srv.URL, nil, nil, WithAuth(AuthConfig{
		Username: testOSUser, Password: testOSPass, SkipVerify: true,
	}))
	if err != nil {
		t.Fatalf("expected success with valid creds + skip-verify, got: %v", err)
	}
}

func TestNew_SkipVerifyRejectsBadCredentials(t *testing.T) {
	srv := authInfoServer(t)
	_, err := New(context.Background(), srv.URL, nil, nil, WithAuth(AuthConfig{
		Username: testOSUser, Password: "wrong", SkipVerify: true,
	}))
	if err == nil {
		t.Fatal("expected connection to fail when credentials are rejected")
	}
}

func TestNew_VerifyOnByDefaultRejectsSelfSignedCert(t *testing.T) {
	srv := authInfoServer(t)
	// Valid creds, but no CA and no skip-verify => default verifying transport,
	// which must reject the server's self-signed certificate.
	_, err := New(context.Background(), srv.URL, nil, nil, WithAuth(AuthConfig{
		Username: testOSUser, Password: testOSPass,
	}))
	if err == nil {
		t.Fatal("expected TLS verification to reject an untrusted self-signed cert")
	}
}

func TestNew_CAFileVerifiesServerCert(t *testing.T) {
	srv := authInfoServer(t)

	// Pin the server's own cert as the CA bundle: verification is ON and must
	// succeed because 127.0.0.1 is in the httptest cert's SANs.
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(context.Background(), srv.URL, nil, nil, WithAuth(AuthConfig{
		Username: testOSUser, Password: testOSPass, CAFile: caPath,
	}))
	if err != nil {
		t.Fatalf("expected success with CA-pinned verification, got: %v", err)
	}
}
