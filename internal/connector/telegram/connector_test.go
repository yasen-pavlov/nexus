package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/connector"
)

func TestConfigure(t *testing.T) {
	c := &Connector{}

	t.Run("valid config", func(t *testing.T) {
		err := c.Configure(connector.Config{
			"name":     "my-tg",
			"api_id":   "12345",
			"api_hash": "abcdef",
			"phone":    "+1234567890",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Name() != "my-tg" {
			t.Errorf("expected name 'my-tg', got %q", c.Name())
		}
		if c.Type() != "telegram" {
			t.Errorf("expected type 'telegram', got %q", c.Type())
		}
		if c.apiID != 12345 {
			t.Errorf("expected api_id 12345, got %d", c.apiID)
		}
	})

	t.Run("api_id as float64", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{
			"api_id": float64(99999), "api_hash": "x", "phone": "+1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if c2.apiID != 99999 {
			t.Errorf("expected 99999, got %d", c2.apiID)
		}
	})

	t.Run("missing api_id", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_hash": "x", "phone": "+1"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing api_hash", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_id": "123", "phone": "+1"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing phone", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_id": "123", "api_hash": "x"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid api_id", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_id": "notanumber", "api_hash": "x", "phone": "+1"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("chat filter", func(t *testing.T) {
		c2 := &Connector{}
		c2.Configure(connector.Config{ //nolint:errcheck // test
			"api_id": "1", "api_hash": "x", "phone": "+1",
			"chat_filter": "Family, Work",
		})
		if len(c2.chatFilter) != 2 {
			t.Errorf("expected 2 filters, got %d", len(c2.chatFilter))
		}
	})

	t.Run("default name", func(t *testing.T) {
		c2 := &Connector{}
		c2.Configure(connector.Config{"api_id": "1", "api_hash": "x", "phone": "+1"}) //nolint:errcheck // test
		if c2.Name() != "telegram" {
			t.Errorf("expected default name 'telegram', got %q", c2.Name())
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := &Connector{apiID: 123, apiHash: "abc", phone: "+1"}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		c := &Connector{}
		if err := c.Validate(); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestMatchesChatFilter(t *testing.T) {
	t.Run("no filter", func(t *testing.T) {
		c := &Connector{}
		if !c.matchesChatFilter("any", "123") {
			t.Error("expected match with no filter")
		}
	})

	t.Run("name match", func(t *testing.T) {
		c := &Connector{chatFilter: []string{"Family"}}
		if !c.matchesChatFilter("Family", "123") {
			t.Error("expected match on name")
		}
		if !c.matchesChatFilter("family", "123") {
			t.Error("expected case-insensitive match")
		}
	})

	t.Run("id match", func(t *testing.T) {
		c := &Connector{chatFilter: []string{"456"}}
		if !c.matchesChatFilter("Other", "456") {
			t.Error("expected match on id")
		}
	})

	t.Run("no match", func(t *testing.T) {
		c := &Connector{chatFilter: []string{"Family"}}
		if c.matchesChatFilter("Work", "789") {
			t.Error("expected no match")
		}
	})
}

func TestHelpers(t *testing.T) {
	// Test userDisplayName
	tests := []struct {
		first, last, username, want string
	}{
		{"John", "Doe", "jd", "John Doe"},
		{"John", "", "jd", "John"},
		{"", "", "jd", "jd"},
	}
	for _, tt := range tests {
		u := &tg.User{FirstName: tt.first, LastName: tt.last, Username: tt.username}
		got := userDisplayName(u)
		if got != tt.want {
			t.Errorf("userDisplayName(%q,%q,%q) = %q, want %q", tt.first, tt.last, tt.username, got, tt.want)
		}
	}
}
