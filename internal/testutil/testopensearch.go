package testutil

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// TestOSConfig returns the OpenSearch URL and a unique test index name.
// Tests should use this to create their own search.Client via search.NewWithIndex.
func TestOSConfig(t *testing.T, prefix string) (url, index string) {
	t.Helper()

	url = os.Getenv("NEXUS_TEST_OPENSEARCH_URL")
	if url == "" {
		url = "http://localhost:9200"
	}

	index = fmt.Sprintf("nexus-test-%s-%d", prefix, rand.Int63()) //nolint:gosec // test index name
	return url, index
}
