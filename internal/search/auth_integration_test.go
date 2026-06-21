//go:build integration

package search

import (
	"context"
	"os"
	"testing"

	"github.com/muty/nexus/internal/lang"
)

// TestAuth_AgainstSecuredCluster exercises the real auth + TLS path against a
// security-plugin-enabled OpenSearch. It is skipped unless a secured cluster is
// provided via env vars, so it never runs in the default (unsecured) harness:
//
//	NEXUS_TEST_SECURE_OPENSEARCH_URL=https://localhost:19200 \
//	NEXUS_TEST_SECURE_OPENSEARCH_PASSWORD=... \
//	go test -tags integration -run TestAuth_AgainstSecuredCluster ./internal/search/
func TestAuth_AgainstSecuredCluster(t *testing.T) {
	url := os.Getenv("NEXUS_TEST_SECURE_OPENSEARCH_URL")
	pass := os.Getenv("NEXUS_TEST_SECURE_OPENSEARCH_PASSWORD")
	if url == "" || pass == "" {
		t.Skip("set NEXUS_TEST_SECURE_OPENSEARCH_URL + NEXUS_TEST_SECURE_OPENSEARCH_PASSWORD to run")
	}
	user := os.Getenv("NEXUS_TEST_SECURE_OPENSEARCH_USERNAME")
	if user == "" {
		user = "admin"
	}
	ctx := context.Background()

	t.Run("rejects missing credentials", func(t *testing.T) {
		if _, err := New(ctx, url, nil, nil, WithAuth(AuthConfig{SkipVerify: true})); err == nil {
			t.Fatal("expected connection to a secured cluster to fail without credentials")
		}
	})

	t.Run("rejects wrong credentials", func(t *testing.T) {
		if _, err := New(ctx, url, nil, nil, WithAuth(AuthConfig{Username: user, Password: "definitely-wrong", SkipVerify: true})); err == nil {
			t.Fatal("expected connection to fail with wrong credentials")
		}
	})

	t.Run("connects and round-trips with valid credentials", func(t *testing.T) {
		c, err := NewWithIndex(ctx, url, "nexus-auth-it", nil, lang.Default(),
			WithAuth(AuthConfig{Username: user, Password: pass, SkipVerify: true}))
		if err != nil {
			t.Fatalf("connect with valid creds: %v", err)
		}
		if err := c.EnsureIndex(ctx, 0); err != nil {
			t.Fatalf("ensure index over authenticated TLS: %v", err)
		}
		t.Cleanup(func() { c.DeleteIndex(context.Background()) }) //nolint:errcheck // test cleanup
	})
}
