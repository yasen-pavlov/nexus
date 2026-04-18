package api

import (
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func lineMeta(id int64, text, senderName, avatarKey string, senderID int64, createdAt string) map[string]any {
	m := map[string]any{
		"id":         float64(id), // float64 because JSON decoders land numbers here
		"text":       text,
		"created_at": createdAt,
	}
	if senderID != 0 {
		m["sender_id"] = float64(senderID)
	}
	if senderName != "" {
		m["sender_name"] = senderName
	}
	if avatarKey != "" {
		m["sender_avatar_key"] = avatarKey
	}
	return m
}

func hitWith(content, headline, conversationID string, lines []any) *model.DocumentHit {
	hit := &model.DocumentHit{
		Rank:     1,
		Headline: headline,
	}
	hit.SourceType = "telegram"
	hit.Content = content
	hit.ConversationID = conversationID
	hit.Metadata = map[string]any{"message_lines": lines}
	return hit
}

func TestResolveWindowMatch_SimpleMiddleLine(t *testing.T) {
	lines := []any{
		lineMeta(100, "hello world", "Alice", "avatars:1", 1, "2026-04-08T10:00:00Z"),
		lineMeta(101, "dinner at seven", "Bob", "avatars:2", 2, "2026-04-08T10:05:00Z"),
		lineMeta(102, "see you there", "Alice", "avatars:1", 1, "2026-04-08T10:06:00Z"),
	}
	content := "hello world\ndinner at seven\nsee you there"
	headline := "dinner at <mark>seven</mark>"
	hit := hitWith(content, headline, "group-42", lines)

	m := resolveWindowMatch(hit)
	if m == nil {
		t.Fatal("expected match, got nil")
	}
	if m.MessageID != 101 {
		t.Errorf("MessageID = %d, want 101", m.MessageID)
	}
	if m.SenderName != "Bob" {
		t.Errorf("SenderName = %q, want Bob", m.SenderName)
	}
	if m.SourceID != "group-42:101:msg" {
		t.Errorf("SourceID = %q, want group-42:101:msg", m.SourceID)
	}
	if m.AvatarKey != "avatars:2" {
		t.Errorf("AvatarKey = %q, want avatars:2", m.AvatarKey)
	}
	if m.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be parsed")
	}
}

func TestResolveWindowMatch_FirstLine(t *testing.T) {
	lines := []any{
		lineMeta(100, "hello world", "Alice", "", 1, "2026-04-08T10:00:00Z"),
		lineMeta(101, "second message", "Bob", "", 2, "2026-04-08T10:05:00Z"),
	}
	hit := hitWith("hello world\nsecond message", "<mark>hello</mark> world", "c", lines)
	m := resolveWindowMatch(hit)
	if m == nil || m.MessageID != 100 {
		t.Errorf("expected message 100, got %+v", m)
	}
}

func TestResolveWindowMatch_LastLine(t *testing.T) {
	lines := []any{
		lineMeta(100, "alpha", "A", "", 1, "2026-04-08T10:00:00Z"),
		lineMeta(101, "beta gamma", "B", "", 2, "2026-04-08T10:05:00Z"),
	}
	hit := hitWith("alpha\nbeta gamma", "beta <mark>gamma</mark>", "c", lines)
	m := resolveWindowMatch(hit)
	if m == nil || m.MessageID != 101 {
		t.Errorf("expected message 101, got %+v", m)
	}
}

func TestResolveWindowMatch_CyrillicBytes(t *testing.T) {
	// Cyrillic characters are 2 bytes each in UTF-8. The mapping must
	// treat offsets as byte positions, not rune positions — otherwise
	// we'd land on the wrong line for any query term past the first
	// multi-byte character.
	lines := []any{
		lineMeta(1, "Имаме си Църчо", "Y", "", 100, "2026-04-08T10:00:00Z"),
		lineMeta(2, "Вече не е толкова червена", "M", "", 200, "2026-04-08T10:05:00Z"),
		lineMeta(3, "По малкото пипонче", "Y", "", 100, "2026-04-08T10:10:00Z"),
	}
	content := "Имаме си Църчо\nВече не е толкова червена\nПо малкото пипонче"
	headline := "По малкото <mark>пипонче</mark>"
	hit := hitWith(content, headline, "chat", lines)

	m := resolveWindowMatch(hit)
	if m == nil {
		t.Fatal("expected match on Cyrillic content")
	}
	if m.MessageID != 3 {
		t.Errorf("MessageID = %d, want 3 (the пипонче line)", m.MessageID)
	}
}

func TestResolveWindowMatch_NilCases(t *testing.T) {
	t.Run("non-telegram hit", func(t *testing.T) {
		hit := &model.DocumentHit{Rank: 1, Headline: "<mark>x</mark>"}
		hit.SourceType = "imap"
		if m := resolveWindowMatch(hit); m != nil {
			t.Errorf("expected nil for non-telegram, got %+v", m)
		}
	})

	t.Run("empty headline", func(t *testing.T) {
		hit := hitWith("content", "", "c", []any{lineMeta(1, "x", "A", "", 1, "")})
		if m := resolveWindowMatch(hit); m != nil {
			t.Errorf("expected nil when headline is empty")
		}
	})

	t.Run("no message_lines", func(t *testing.T) {
		hit := &model.DocumentHit{Rank: 1, Headline: "<mark>x</mark>"}
		hit.SourceType = "telegram"
		hit.Content = "x"
		hit.Metadata = map[string]any{}
		if m := resolveWindowMatch(hit); m != nil {
			t.Errorf("expected nil without message_lines")
		}
	})

	t.Run("no mark tag in headline", func(t *testing.T) {
		lines := []any{lineMeta(1, "hello", "A", "", 1, "2026-04-08T10:00:00Z")}
		hit := hitWith("hello", "hello", "c", lines)
		if m := resolveWindowMatch(hit); m != nil {
			t.Errorf("expected nil without <mark>")
		}
	})

	t.Run("fragment not found in content", func(t *testing.T) {
		lines := []any{lineMeta(1, "hello", "A", "", 1, "2026-04-08T10:00:00Z")}
		// Highlight references a term that isn't in content — shouldn't happen in practice
		// but verify the guard.
		hit := hitWith("hello", "goodbye <mark>world</mark>", "c", lines)
		if m := resolveWindowMatch(hit); m != nil {
			t.Errorf("expected nil when fragment doesn't appear in content")
		}
	})
}

func TestApplyWindowMatches_PopulatesFields(t *testing.T) {
	lines := []any{
		lineMeta(100, "hello", "Alice", "avatars:1", 1, "2026-04-08T10:00:00Z"),
		lineMeta(101, "world match", "Bob", "avatars:2", 2, "2026-04-08T10:05:00Z"),
	}
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{
				Rank:     1,
				Headline: "world <mark>match</mark>",
				Document: model.Document{
					SourceType:     "telegram",
					Content:        "hello\nworld match",
					ConversationID: "c42",
					Metadata:       map[string]any{"message_lines": lines},
				},
			},
		},
	}

	applyWindowMatches(result)

	hit := result.Documents[0]
	if hit.MatchMessageID != 101 {
		t.Errorf("MatchMessageID = %d, want 101", hit.MatchMessageID)
	}
	if hit.MatchSenderName != "Bob" {
		t.Errorf("MatchSenderName = %q, want Bob", hit.MatchSenderName)
	}
	if hit.MatchSourceID != "c42:101:msg" {
		t.Errorf("MatchSourceID = %q, want c42:101:msg", hit.MatchSourceID)
	}
	if hit.MatchCreatedAt == nil {
		t.Errorf("MatchCreatedAt should be non-nil")
	}
}

func TestApplyWindowMatches_LeavesSemanticHitUntouched(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{
				Rank:     1,
				Headline: "", // semantic-only, no highlight
				Document: model.Document{
					SourceType:     "telegram",
					Content:        "hello\nworld",
					ConversationID: "c42",
					Metadata: map[string]any{
						"message_lines": []any{
							lineMeta(100, "hello", "A", "", 1, "2026-04-08T10:00:00Z"),
						},
					},
				},
			},
		},
	}
	applyWindowMatches(result)
	if result.Documents[0].MatchMessageID != 0 {
		t.Errorf("MatchMessageID should stay zero for semantic-only hits")
	}
}

func TestReadInt64(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int64
		wantOK bool
	}{
		{"float64", float64(42), 42, true},
		{"float64 truncates", float64(42.9), 42, true},
		{"int64", int64(100), 100, true},
		{"int", int(7), 7, true},
		{"string rejected", "42", 0, false},
		{"nil rejected", nil, 0, false},
		{"bool rejected", true, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := readInt64(tc.in)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("value = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFormatInt64(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{-1, "-1"},
		{9223372036854775807, "9223372036854775807"},
	}
	for _, tc := range cases {
		if got := formatInt64(tc.in); got != tc.want {
			t.Errorf("formatInt64(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	_ = time.Now
}
