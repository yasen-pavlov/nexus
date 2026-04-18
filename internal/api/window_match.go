package api

import (
	"strings"
	"time"

	"github.com/muty/nexus/internal/model"
)

// windowMatch is the resolved pointer from a window-doc search hit to
// the specific message line inside the window that the BM25 highlight
// selected. Populated on Telegram window hits when a confident match
// can be pinpointed; nil for semantic-only hits (no highlight → no
// attribution).
type windowMatch struct {
	SourceID       string
	MessageID      int64
	CreatedAt      time.Time
	SenderID       int64
	SenderName     string
	AvatarKey      string
	ConversationID string
}

// resolveWindowMatch maps a window-doc search hit's highlight fragment
// back to the specific message_lines entry that produced it. Returns
// nil when the hit isn't a telegram window, has no highlight, lacks
// message_lines metadata, or when the fragment can't be located in
// content (any of which fall through to semantic-fallback rendering on
// the frontend).
//
// Why this works: OpenSearch's highlighter returns a fragment of
// hit.Content with <mark>…</mark> around matched terms. Content is the
// "\n"-joined concatenation of window messages; message_lines carries
// each message's text in the same order. We locate the first <mark>
// inside the fragment, then find the fragment inside content, giving
// us an absolute byte offset for the match. Walking message_lines with
// a running offset (len(text)+1 per entry, the "\n" separator) maps
// offset → message.
//
// All arithmetic is on bytes — Go's len(string) returns bytes, and
// OpenSearch reports byte offsets. Multi-byte UTF-8 (Cyrillic, emoji)
// is handled correctly as long as we stay on byte math end-to-end.
func resolveWindowMatch(hit *model.DocumentHit) *windowMatch {
	if hit == nil || hit.SourceType != "telegram" {
		return nil
	}
	if hit.Headline == "" || hit.Content == "" {
		return nil
	}
	linesRaw, ok := hit.Metadata["message_lines"]
	if !ok {
		return nil
	}
	lines, ok := linesRaw.([]any)
	if !ok || len(lines) == 0 {
		return nil
	}

	markStart := strings.Index(hit.Headline, "<mark>")
	if markStart < 0 {
		return nil
	}

	// The fragment's position within content tells us where the match
	// sits in the window. Strip all tags (there can be multiple <mark>
	// pairs if the query has multiple terms) so the substring search
	// matches the raw content.
	stripped := stripMarkTags(hit.Headline)
	// The offset of the first <mark> inside the stripped fragment equals
	// its position inside the fragment-as-substring-of-content, because
	// there are no tags before it in stripped form.
	strippedMarkOffset := markStart

	fragStart := strings.Index(hit.Content, stripped)
	if fragStart < 0 {
		// OpenSearch may return a fragment that's been normalized
		// (e.g. whitespace collapsing) — the round-trip isn't always
		// byte-exact. Bail rather than misattribute.
		return nil
	}

	matchOffset := fragStart + strippedMarkOffset

	var cursor int
	for i, raw := range lines {
		line, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text, _ := line["text"].(string)
		end := cursor + len(text)
		if matchOffset >= cursor && matchOffset < end {
			return lineToMatch(line, hit.ConversationID)
		}
		// +1 for the "\n" separator between entries. The last entry
		// has no trailing separator in content, but that edge only
		// matters if matchOffset falls past the final line — which
		// the loop check already rejects.
		cursor = end + 1
		_ = i
	}
	return nil
}

// lineToMatch projects a message_lines entry into the windowMatch
// shape the search handler writes onto the DocumentHit. Uses loose
// type assertions because metadata comes back from OpenSearch as
// map[string]any with JSON-number coercion rules.
func lineToMatch(line map[string]any, conversationID string) *windowMatch {
	wm := &windowMatch{ConversationID: conversationID}

	if id, ok := readInt64(line["id"]); ok {
		wm.MessageID = id
	}
	if senderID, ok := readInt64(line["sender_id"]); ok {
		wm.SenderID = senderID
	}
	if name, ok := line["sender_name"].(string); ok {
		wm.SenderName = name
	}
	if key, ok := line["sender_avatar_key"].(string); ok {
		wm.AvatarKey = key
	}
	if createdStr, ok := line["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
			wm.CreatedAt = t
		}
	}
	// Compose the per-message source_id in the same format the
	// telegram connector emits (chat_id:message_id:msg) so the
	// frontend can pass it straight to /api/documents/by-source for
	// lazy reply-target fetches.
	if wm.MessageID != 0 && conversationID != "" {
		wm.SourceID = conversationID + ":" + formatInt64(wm.MessageID) + ":msg"
	}
	return wm
}

// readInt64 accepts the numeric forms JSON decoders produce: float64
// (encoding/json default), json.Number, int, int64. Returns ok=false
// for anything else.
func readInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

// formatInt64 is a strconv.FormatInt shim kept local to avoid pulling
// strconv into a file that otherwise just does string work.
func formatInt64(n int64) string {
	// len 20 covers -9223372036854775808 through 9223372036854775807
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// stripMarkTags removes every <mark>/</mark> tag from a highlight
// fragment, leaving the underlying text unchanged. Using a simple
// string replace because the tags are chosen by us (see
// search.highlightConfig) and aren't user-controllable — no escaping
// concerns.
func stripMarkTags(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	return s
}

// applyWindowMatches runs resolveWindowMatch across every hit and
// populates the DocumentHit.Match* fields when a resolution succeeds.
// Leaves semantic-only hits untouched so the frontend can render a
// bookended preview from the existing message_lines metadata.
func applyWindowMatches(result *model.SearchResult) {
	if result == nil {
		return
	}
	for i := range result.Documents {
		m := resolveWindowMatch(&result.Documents[i])
		if m == nil {
			continue
		}
		result.Documents[i].MatchSourceID = m.SourceID
		result.Documents[i].MatchMessageID = m.MessageID
		if !m.CreatedAt.IsZero() {
			t := m.CreatedAt
			result.Documents[i].MatchCreatedAt = &t
		}
		result.Documents[i].MatchSenderID = m.SenderID
		result.Documents[i].MatchSenderName = m.SenderName
		result.Documents[i].MatchAvatarKey = m.AvatarKey
	}
}
