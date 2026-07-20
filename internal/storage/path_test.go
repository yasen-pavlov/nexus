package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestContained(t *testing.T) {
	base := filepath.FromSlash("/store")
	cases := []struct {
		name string
		dst  string
		ok   bool
	}{
		{"inside", filepath.Join(base, "imap", "mail", "abc.bin"), true},
		{"base itself", base, true},
		{"escape via join", filepath.Join(base, "imap", "..", "..", "etc", "x.bin"), false},
		{"sibling prefix trap", base + "-evil", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := contained(base, tc.dst)
			if tc.ok && err != nil {
				t.Fatalf("contained(%q) = err %v, want ok", tc.dst, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("contained(%q) = %q, want error (escapes root)", tc.dst, got)
			}
		})
	}
}

// TestKeyPath_TraversingNameStaysContained proves the defense-in-depth guard:
// even if a connector name with path separators slipped past handler
// validation, keyPath refuses to hand back a path outside the store root.
func TestKeyPath_TraversingNameStaysContained(t *testing.T) {
	bs := &BinaryStore{basePath: filepath.FromSlash("/store")}

	// Benign name resolves inside the store.
	p, err := bs.keyPath("imap", "mailbox", "folder:42")
	if err != nil {
		t.Fatalf("benign keyPath returned error: %v", err)
	}
	if !strings.HasPrefix(p, filepath.FromSlash("/store")+string(filepath.Separator)) {
		t.Fatalf("benign path %q not under store root", p)
	}

	// A name crafted to climb out is rejected.
	if _, err := bs.keyPath("imap", filepath.FromSlash("../../../../var/lib/nexus"), "x"); err == nil {
		t.Fatal("keyPath accepted a traversing connector name")
	}
}
