//go:build integration

package search

import (
	"context"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

// indexRelChunk is a terse helper for the relations integration tests —
// fills in the bookkeeping fields so each test body only declares what
// actually varies.
func indexRelChunk(t *testing.T, c *Client, ch model.Chunk) {
	t.Helper()
	ctx := context.Background()
	ch.ParentID = ch.SourceType + ":" + ch.SourceName + ":" + ch.SourceID
	if ch.ID == "" {
		ch.ID = ch.ParentID + ":0"
	}
	if ch.DocID == "" {
		ch.DocID = model.DocumentID(ch.SourceType, ch.SourceName, ch.SourceID).String()
	}
	if ch.CreatedAt.IsZero() {
		ch.CreatedAt = time.Now()
	}
	if ch.Visibility == "" {
		ch.Visibility = "private"
	}
	if err := c.IndexChunks(ctx, []model.Chunk{ch}); err != nil {
		t.Fatalf("IndexChunks: %v", err)
	}
}

func TestFindChunksByTerm_MatchesKeywordField(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Seed two emails with distinct Message-IDs. FindChunksByTerm on
	// imap_message_id should return exactly the matching one, dedup'd
	// to first chunk (there's only one here).
	indexRelChunk(t, c, model.Chunk{
		SourceType: "imap", SourceName: "t", SourceID: "INBOX:1",
		Title: "First", Content: "first",
		IMAPMessageID: "one@x",
	})
	indexRelChunk(t, c, model.Chunk{
		SourceType: "imap", SourceName: "t", SourceID: "INBOX:2",
		Title: "Second", Content: "second",
		IMAPMessageID: "two@x",
	})
	_ = c.Refresh(ctx)

	hits, err := c.FindChunksByTerm(ctx, "imap_message_id", "two@x")
	if err != nil {
		t.Fatalf("FindChunksByTerm: %v", err)
	}
	if len(hits) != 1 || hits[0].SourceID != "INBOX:2" {
		t.Fatalf("expected one hit for INBOX:2, got %+v", hits)
	}

	// Unknown value → empty result, no error (used as a signal by the
	// /related handler when a target Message-ID isn't indexed yet).
	none, err := c.FindChunksByTerm(ctx, "imap_message_id", "missing@x")
	if err != nil || len(none) != 0 {
		t.Fatalf("expected empty, got (%v, %v)", none, err)
	}
}

func TestFindChunksByTerm_EmptyInput(t *testing.T) {
	c := newTestClient(t)
	// Empty field or value returns nil without ever hitting OpenSearch —
	// guards callers from building malformed term queries.
	for _, tc := range []struct{ field, value string }{
		{"", "foo"}, {"source_id", ""}, {"", ""},
	} {
		hits, err := c.FindChunksByTerm(context.Background(), tc.field, tc.value)
		if err != nil || hits != nil {
			t.Errorf("(%q,%q): expected nil/nil, got (%v,%v)", tc.field, tc.value, hits, err)
		}
	}
}

func TestFindChunksReferencing_MatchesNestedTargets(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Two chunks pointing at the same email via attachment_of — one by
	// target_id (UUID), one by target_source_id. Both should surface.
	emailDocID := model.DocumentID("imap", "t", "INBOX:42").String()
	indexRelChunk(t, c, model.Chunk{
		SourceType: "imap", SourceName: "t", SourceID: "INBOX:42:attachment:0",
		Title: "a.txt", Content: "first",
		Relations: []model.Relation{{
			Type: model.RelationAttachmentOf, TargetID: emailDocID,
		}},
	})
	indexRelChunk(t, c, model.Chunk{
		SourceType: "imap", SourceName: "t", SourceID: "INBOX:42:attachment:1",
		Title: "b.txt", Content: "second",
		Relations: []model.Relation{{
			Type: model.RelationAttachmentOf, TargetSourceID: "INBOX:42",
		}},
	})
	// Decoy chunk whose relations point elsewhere — must not match.
	indexRelChunk(t, c, model.Chunk{
		SourceType: "imap", SourceName: "t", SourceID: "INBOX:99:attachment:0",
		Title: "decoy.txt", Content: "nope",
		Relations: []model.Relation{{
			Type: model.RelationAttachmentOf, TargetSourceID: "INBOX:99",
		}},
	})
	_ = c.Refresh(ctx)

	// Query by docID OR source_id — both backrefs should land.
	hits, err := c.FindChunksReferencing(ctx, []string{emailDocID}, []string{"INBOX:42"})
	if err != nil {
		t.Fatalf("FindChunksReferencing: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 references, got %d (%+v)", len(hits), hits)
	}
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.SourceID] = true
	}
	for _, want := range []string{"INBOX:42:attachment:0", "INBOX:42:attachment:1"} {
		if !seen[want] {
			t.Errorf("missing expected hit %q (got %v)", want, seen)
		}
	}
}

func TestFindChunksReferencing_EmptyInput(t *testing.T) {
	c := newTestClient(t)
	hits, err := c.FindChunksReferencing(context.Background(), nil, nil)
	if err != nil || hits != nil {
		t.Errorf("expected nil/nil for empty targets, got (%v,%v)", hits, err)
	}
}

func TestGetConversationMessages_DirectionAndFilters(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Seed 5 message chunks with strictly-increasing timestamps. Also
	// seed a window doc (non-hidden) — it must never surface in the
	// result since the helper enforces hidden=true.
	base := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	for i := 0; i < 5; i++ {
		indexRelChunk(t, c, model.Chunk{
			SourceType: "telegram", SourceName: "t",
			SourceID: "9:" + intStr(1000+i) + ":msg",
			Title:    "m", Content: "message " + intStr(i),
			ConversationID: "9", Hidden: true,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	indexRelChunk(t, c, model.Chunk{
		SourceType: "telegram", SourceName: "t", SourceID: "9:1000-1004",
		Title: "Window", Content: "joined", ConversationID: "9",
	})
	_ = c.Refresh(ctx)

	// No cursor → tail N, chronologically. With 5 messages and limit=3
	// the tail is the last 3 (1002, 1003, 1004) in ASC order.
	tail, err := c.GetConversationMessages(ctx, ConversationMessagesOptions{
		SourceType: "telegram", Conversation: "9", Limit: 3,
	})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 3 || tail[0].SourceID != "9:1002:msg" || tail[2].SourceID != "9:1004:msg" {
		t.Fatalf("tail: expected 1002..1004 ASC, got %+v", tail)
	}
	// Window doc must be filtered out by hidden=true.
	for _, m := range tail {
		if !m.Hidden {
			t.Errorf("non-hidden chunk leaked into result: %+v", m)
		}
	}

	// `before` cursor → older N before the cutoff, ASC.
	older, err := c.GetConversationMessages(ctx, ConversationMessagesOptions{
		SourceType: "telegram", Conversation: "9", Limit: 2,
		Before: tail[0].CreatedAt,
	})
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	if len(older) != 2 || older[0].SourceID != "9:1000:msg" || older[1].SourceID != "9:1001:msg" {
		t.Fatalf("before: expected 1000..1001 ASC, got %+v", older)
	}

	// `after` cursor → newer messages strictly after, ASC.
	newer, err := c.GetConversationMessages(ctx, ConversationMessagesOptions{
		SourceType: "telegram", Conversation: "9", Limit: 10,
		After: tail[0].CreatedAt,
	})
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	if len(newer) != 2 {
		t.Fatalf("after: expected 2 newer messages, got %d", len(newer))
	}
	for _, m := range newer {
		if !m.CreatedAt.After(tail[0].CreatedAt) {
			t.Errorf("after-cursor leak: %v <= %v", m.CreatedAt, tail[0].CreatedAt)
		}
	}
}

func TestGetConversationMessages_UnknownConversation(t *testing.T) {
	c := newTestClient(t)
	// An unknown conversation id returns an empty slice, not an error —
	// this is the "non-chat connector" code path the chat-browser UI
	// relies on to render an empty state cleanly.
	got, err := c.GetConversationMessages(context.Background(), ConversationMessagesOptions{
		SourceType: "telegram", Conversation: "nope", Limit: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

// intStr is a tiny helper — strconv.Itoa would work but keeping the call
// sites compact is worth the 3-line helper in this test file.
func intStr(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
