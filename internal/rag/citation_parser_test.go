package rag

import (
	"strings"
	"testing"
)

func docs3() []ParserDoc {
	return []ParserDoc{
		{DocID: "a"}, {DocID: "b"}, {DocID: "c"},
	}
}

func TestCitationParser_CyrillicPrefixUTF16Span(t *testing.T) {
	p := NewCitationParser(docs3())
	// "Привет " is 7 characters, each a single UTF-16 code unit (13 bytes).
	// The span must be 7 (UTF-16), not 13 (bytes), so the FE pill lands right.
	out, cites := p.Feed("Привет [1]!")
	if out != "Привет !" {
		t.Errorf("out=%q want %q", out, "Привет !")
	}
	if len(cites) != 1 {
		t.Fatalf("want 1 citation, got %d", len(cites))
	}
	if cites[0].SpanStart != 7 || cites[0].SpanEnd != 7 {
		t.Errorf("span=[%d,%d] want [7,7]", cites[0].SpanStart, cites[0].SpanEnd)
	}
}

func TestCitationParserAt_SeededCursor(t *testing.T) {
	// Seed the cursor at 10 (representing prior rounds' text). "ok " is 3 UTF-16
	// units, so the [1] marker closes at 10+3=13 — an offset into the whole-turn
	// answer, not this round's local offset of 3.
	p := NewCitationParserAt(docs3(), 10)
	out, cites := p.Feed("ok [1].")
	if out != "ok ." {
		t.Errorf("out=%q want %q", out, "ok .")
	}
	if len(cites) != 1 {
		t.Fatalf("want 1 citation, got %d", len(cites))
	}
	if cites[0].SpanStart != 13 || cites[0].SpanEnd != 13 {
		t.Errorf("span=[%d,%d] want [13,13] (seed 10 + \"ok \" 3)", cites[0].SpanStart, cites[0].SpanEnd)
	}
}

func TestCitationParser_EmojiPrefixSurrogatePair(t *testing.T) {
	p := NewCitationParser(docs3())
	// "👍 " is 3 UTF-16 code units (emoji is a surrogate pair = 2, plus space).
	_, cites := p.Feed("👍 [2] done")
	if len(cites) != 1 {
		t.Fatalf("want 1 citation, got %d", len(cites))
	}
	if cites[0].SpanStart != 3 {
		t.Errorf("span=%d want 3", cites[0].SpanStart)
	}
	if cites[0].DocID != "b" {
		t.Errorf("docID=%q want b", cites[0].DocID)
	}
}

func TestCitationParser_MultibyteRuneSplitAcrossFeeds(t *testing.T) {
	p := NewCitationParser(docs3())
	// Split the 2-byte 'и' across two deltas; the cursor must still count it
	// as a single UTF-16 unit. "ии" = 2 units, then "[1]" closes at 2.
	b := []byte("ии[1]")
	out1, _ := p.Feed(string(b[:3])) // "и" + first byte of second "и"
	out2, cites := p.Feed(string(b[3:]))
	if got := out1 + out2; got != "ии" {
		t.Errorf("out=%q want %q", got, "ии")
	}
	if len(cites) != 1 || cites[0].SpanStart != 2 {
		t.Fatalf("cites=%v want one span at 2", cites)
	}
}

func TestCitationParser_PassesPlainText(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("hello world")
	if out != "hello world" {
		t.Errorf("out=%q", out)
	}
	if len(cites) != 0 {
		t.Errorf("cites=%v", cites)
	}
}

func TestCitationParser_SingleMarker(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("foo [1] bar")
	if out != "foo  bar" {
		t.Errorf("out=%q", out)
	}
	if len(cites) != 1 {
		t.Fatalf("cites=%v", cites)
	}
	if cites[0].DocID != "a" {
		t.Errorf("docid=%q", cites[0].DocID)
	}
	// Marker closes at offset 4 (after "foo "). SpanStart should be 4.
	if cites[0].SpanStart != 4 || cites[0].SpanEnd != 4 {
		t.Errorf("span=[%d,%d] want [4,4]", cites[0].SpanStart, cites[0].SpanEnd)
	}
}

func TestCitationParser_AdjacentMarkers(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("foo[1][3].")
	if out != "foo." {
		t.Errorf("out=%q", out)
	}
	if len(cites) != 2 {
		t.Fatalf("cites=%d", len(cites))
	}
	if cites[0].DocID != "a" || cites[1].DocID != "c" {
		t.Errorf("ids=%q,%q", cites[0].DocID, cites[1].DocID)
	}
	// Both citations land at cursor=3 (after "foo").
	for i, c := range cites {
		if c.SpanStart != 3 {
			t.Errorf("c[%d].SpanStart=%d want 3", i, c.SpanStart)
		}
	}
}

func TestCitationParser_MultiDigitMarker(t *testing.T) {
	docs := make([]ParserDoc, 12)
	for i := range docs {
		docs[i] = ParserDoc{DocID: string(rune('a' + i))}
	}
	p := NewCitationParser(docs)
	out, cites := p.Feed("see [12] and done")
	if out != "see  and done" {
		t.Errorf("out=%q", out)
	}
	if len(cites) != 1 || cites[0].DocID != "l" {
		t.Errorf("cites=%v", cites)
	}
}

func TestCitationParser_OutOfRangeFlushedAsText(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("oops [99] here")
	// Out-of-range markers stay in the text so the user sees what the
	// model emitted — they're broken but not invisible.
	if !strings.Contains(out, "[99]") {
		t.Errorf("expected [99] kept in out, got %q", out)
	}
	if len(cites) != 0 {
		t.Errorf("expected no citations, got %v", cites)
	}
}

func TestCitationParser_MalformedMarkerFlushedAsText(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("text [abc] more")
	// `[abc]` is not a valid marker — `[` flushes as text and the rest
	// also flushes through.
	if out != "text [abc] more" {
		t.Errorf("out=%q", out)
	}
	if len(cites) != 0 {
		t.Errorf("cites=%v", cites)
	}
}

func TestCitationParser_EmptyBracketsFlushed(t *testing.T) {
	p := NewCitationParser(docs3())
	out, _ := p.Feed("foo [] bar")
	if out != "foo [] bar" {
		t.Errorf("out=%q", out)
	}
}

func TestCitationParser_StreamingSplitMidMarker(t *testing.T) {
	p := NewCitationParser(docs3())
	out1, cites1 := p.Feed("hello [")
	if out1 != "hello " {
		t.Errorf("out1=%q", out1)
	}
	if len(cites1) != 0 {
		t.Errorf("cites1=%v", cites1)
	}

	out2, cites2 := p.Feed("1] world")
	if out2 != " world" {
		t.Errorf("out2=%q", out2)
	}
	if len(cites2) != 1 || cites2[0].DocID != "a" {
		t.Errorf("cites2=%v", cites2)
	}
	// Cursor should be 6 (after "hello ") at the citation.
	if cites2[0].SpanStart != 6 {
		t.Errorf("span=%d want 6", cites2[0].SpanStart)
	}
}

func TestCitationParser_SplitAcrossMultipleFeeds(t *testing.T) {
	p := NewCitationParser(docs3())
	parts := []string{"a", "[", "2", "]", "b"}
	var allOut strings.Builder
	var allCites int
	for _, s := range parts {
		o, c := p.Feed(s)
		allOut.WriteString(o)
		allCites += len(c)
	}
	if allOut.String() != "ab" {
		t.Errorf("out=%q", allOut.String())
	}
	if allCites != 1 {
		t.Errorf("cites=%d", allCites)
	}
}

func TestCitationParser_DanglingPartialMarkerOnFlush(t *testing.T) {
	p := NewCitationParser(docs3())
	out, _ := p.Feed("done [1")
	if out != "done " {
		t.Errorf("out=%q", out)
	}
	flushed := p.Flush()
	if flushed != "[1" {
		t.Errorf("flushed=%q", flushed)
	}
}

func TestCitationParser_FlushOnNoPending(t *testing.T) {
	p := NewCitationParser(docs3())
	if got := p.Flush(); got != "" {
		t.Errorf("Flush=%q", got)
	}
}

func TestCitationParser_EmptyInput(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("")
	if out != "" {
		t.Errorf("out=%q", out)
	}
	if cites != nil {
		t.Errorf("cites=%v", cites)
	}
}

func TestCitationParser_LongUnterminatedBracketBailsOut(t *testing.T) {
	// Pathological input: `[` followed by many non-digit non-`]` bytes.
	// Parser must not buffer indefinitely.
	p := NewCitationParser(docs3())
	out, _ := p.Feed("[" + strings.Repeat("a", 20))
	// scanMarker bails after 9 chars without `]`. Once it bails, the
	// `[` flushes as text and the remaining bytes flush normally.
	if !strings.HasPrefix(out, "[") {
		t.Errorf("expected [ flushed at start, got %q", out)
	}
}

func TestCitationParser_ZeroIndexNotAccepted(t *testing.T) {
	p := NewCitationParser(docs3())
	out, cites := p.Feed("hi [0] there")
	// 1-based indexing means `[0]` is invalid → flushed as text.
	if !strings.Contains(out, "[0]") {
		t.Errorf("[0] should be kept: %q", out)
	}
	if len(cites) != 0 {
		t.Errorf("cites=%v", cites)
	}
}

func TestCitationParser_CarriesCitedText(t *testing.T) {
	docs := []ParserDoc{
		{DocID: "x", CitedText: "the quote"},
	}
	p := NewCitationParser(docs)
	_, cites := p.Feed("[1]")
	if len(cites) != 1 || cites[0].CitedText != "the quote" {
		t.Errorf("cites=%v", cites)
	}
}

func TestCitationParser_NoDocsRejectsAllMarkers(t *testing.T) {
	p := NewCitationParser(nil)
	out, cites := p.Feed("[1][2]")
	// All markers out of range → flushed as text.
	if out != "[1][2]" {
		t.Errorf("out=%q", out)
	}
	if len(cites) != 0 {
		t.Errorf("cites=%v", cites)
	}
}
