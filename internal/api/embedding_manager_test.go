package api

import (
	"testing"
)

func TestOr(t *testing.T) {
	if or("a", "b") != "a" {
		t.Error("expected 'a'")
	}
	if or("", "b") != "b" {
		t.Error("expected 'b'")
	}
	if or("", "") != "" {
		t.Error("expected empty")
	}
}
