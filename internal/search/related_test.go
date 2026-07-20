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

// TestFindChunksByTerm_ReturnsFirstChunk pins that runChunkQuery returns the
// chunk_index-0 inner_hit, not the arbitrary collapse representative. A
// multi-chunk document must resolve to text that starts at the beginning.
func TestFindChunksByTerm_ReturnsFirstChunk(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// One document as three chunks (same source_id → same doc_id, so they
	// collapse). Indexed OUT of chunk_index order so the collapse
	// representative is unlikely to be chunk 0.
	prefix := "imap:t:INBOX:7"
	for _, ci := range []int{2, 0, 1} {
		indexRelChunk(t, c, model.Chunk{
			SourceType: "imap", SourceName: "t", SourceID: "INBOX:7",
			ID:         prefix + ":" + string(rune('0'+ci)),
			ChunkIndex: ci,
			Title:      "Long Email",
			Content:    []string{"chunk-zero", "chunk-one", "chunk-two"}[ci],
		})
	}
	_ = c.Refresh(ctx)

	hits, err := c.FindChunksByTerm(ctx, "source_id", "INBOX:7")
	if err != nil {
		t.Fatalf("FindChunksByTerm: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 collapsed hit, got %d", len(hits))
	}
	if hits[0].ChunkIndex != 0 || hits[0].Content != "chunk-zero" {
		t.Errorf("expected chunk_index 0 / chunk-zero, got index %d / %q", hits[0].ChunkIndex, hits[0].Content)
	}
}

// TestSearchHit_RelatedCountIncludesThreadReplies pins that a search hit's
// related_count counts incoming reply/thread edges, which target the email's
// RFC-2822 Message-ID rather than its folder:UID source_id. Also asserts the
// imap_message_id wire field is now populated on the hit's Document.
func TestSearchHit_RelatedCountIncludesThreadReplies(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Thread-root email: no outgoing relations, keyed by its Message-ID.
	indexRelChunk(t, c, model.Chunk{
		SourceType: "imap", SourceName: "t", SourceID: "INBOX:1",
		Title: "Quarterly", Content: "quarterly report root message",
		IMAPMessageID: "root@x",
	})
	// Two replies pointing at the root via member_of_thread → its Message-ID.
	for i, sid := range []string{"INBOX:2", "INBOX:3"} {
		indexRelChunk(t, c, model.Chunk{
			SourceType: "imap", SourceName: "t", SourceID: sid,
			Title: "Reply", Content: "reply body " + string(rune('a'+i)),
			IMAPMessageID: sid + "@x",
			Relations: []model.Relation{{
				Type: model.RelationMemberOfThread, TargetSourceID: "root@x",
			}},
		})
	}
	_ = c.Refresh(ctx)

	result, err := c.Search(ctx, model.SearchRequest{Query: "quarterly", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var root *model.DocumentHit
	for i := range result.Documents {
		if result.Documents[i].SourceID == "INBOX:1" {
			root = &result.Documents[i]
		}
	}
	if root == nil {
		t.Fatalf("root email not in results: %+v", result.Documents)
	}
	if root.Document.IMAPMessageID != "root@x" {
		t.Errorf("root hit IMAPMessageID = %q, want root@x (wire field must be populated)", root.Document.IMAPMessageID)
	}
	if root.RelatedCount != 2 {
		t.Errorf("root RelatedCount = %d, want 2 (both thread replies)", root.RelatedCount)
	}
}
