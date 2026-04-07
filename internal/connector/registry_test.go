package connector

import "testing"

func TestRegistry(t *testing.T) {
	// Clear registry for test isolation
	oldRegistry := registry
	registry = map[string]Factory{}
	t.Cleanup(func() { registry = oldRegistry })

	Register("test-type", func() Connector { return nil })

	t.Run("list", func(t *testing.T) {
		types := List()
		if len(types) != 1 {
			t.Fatalf("expected 1 type, got %d", len(types))
		}
		if types[0] != "test-type" {
			t.Errorf("expected 'test-type', got %q", types[0])
		}
	})

	t.Run("create known type", func(t *testing.T) {
		_, err := Create("test-type")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("create unknown type", func(t *testing.T) {
		_, err := Create("unknown")
		if err == nil {
			t.Fatal("expected error for unknown type")
		}
	})
}
