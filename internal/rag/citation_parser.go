package rag

import (
	"strconv"
	"strings"

	"github.com/muty/nexus/internal/model"
)

// CitationParser is a streaming state machine that lifts `[N]` markers
// out of a non-Anthropic provider's text stream and turns them into
// citation events. The marker text itself is dropped from the output
// because the frontend renders citation pills via the citation events;
// otherwise both the literal `[N]` and a pill would appear.
//
// Span semantics: SpanStart=SpanEnd=cursor at marker close — citations
// are zero-width pinpoint markers on the post-stripping text. The
// frontend can attach pills at that offset.
//
// Out-of-range or malformed markers (e.g. `[abc]`, `[99]` when only 5
// docs exist) are flushed as plain text and counted toward the cursor,
// so misbehaving models don't lose tokens.
type CitationParser struct {
	docs    []ParserDoc
	cursor  int
	pending []byte
}

// ParserDoc is the minimal shape the parser needs — DocID for citation
// emission and CitedText (optional) for downstream UX. Independent of
// llm.Document so the parser is unit-testable without the llm package.
type ParserDoc struct {
	DocID     string
	CitedText string
}

// NewCitationParser creates a parser over the docs the model was given,
// in 1-based order matching the [N] markers it will emit.
func NewCitationParser(docs []ParserDoc) *CitationParser {
	return &CitationParser{docs: docs}
}

// Feed consumes a chunk of streamed text. Returns the clean text to
// emit downstream and any citations whose markers fully closed inside
// the chunk. Buffers a trailing partial marker (e.g. ending mid-`[1`)
// for the next Feed call so markers aren't split across deltas.
func (p *CitationParser) Feed(s string) (string, []model.ChatCitation) {
	if s == "" {
		return "", nil
	}

	combined := append(p.pending, []byte(s)...)
	p.pending = nil

	var out strings.Builder
	out.Grow(len(combined))
	var cites []model.ChatCitation

	i := 0
	for i < len(combined) {
		c := combined[i]
		if c != '[' {
			out.WriteByte(c)
			p.cursor++
			i++
			continue
		}

		// Look ahead for the closing `]` and decide whether this is a marker.
		closeIdx, allDigits, hasDigits, exhausted := scanMarker(combined, i+1)
		if closeIdx < 0 {
			if exhausted {
				// We walked far enough to know this can't be a marker.
				// Flush the `[` as plain text and continue.
				out.WriteByte('[')
				p.cursor++
				i++
				continue
			}
			// Truly truncated — buffer for the next Feed.
			p.pending = append(p.pending[:0], combined[i:]...)
			break
		}

		if !allDigits || !hasDigits {
			// `[abc]`, `[]`, `[1a]`, etc. — treat as plain text.
			out.WriteByte('[')
			p.cursor++
			i++
			continue
		}

		n, err := strconv.Atoi(string(combined[i+1 : closeIdx]))
		if err != nil || n < 1 || n > len(p.docs) {
			// Out-of-range or unparseable — flush the marker bytes verbatim.
			out.Write(combined[i : closeIdx+1])
			p.cursor += closeIdx + 1 - i
			i = closeIdx + 1
			continue
		}

		// Valid marker — emit citation, drop the bytes from output.
		doc := p.docs[n-1]
		cites = append(cites, model.ChatCitation{
			DocID:     doc.DocID,
			CitedText: doc.CitedText,
			SpanStart: p.cursor,
			SpanEnd:   p.cursor,
		})
		i = closeIdx + 1
	}

	return out.String(), cites
}

// Flush returns any buffered partial marker as plain text. Call exactly
// once after the last Feed. The cursor is advanced so subsequent
// citations (none in Phase 2 after Flush, but be safe) stay aligned.
func (p *CitationParser) Flush() string {
	if len(p.pending) == 0 {
		return ""
	}
	out := string(p.pending)
	p.cursor += len(out)
	p.pending = nil
	return out
}

// scanMarker walks from `start` until it finds a closing `]` or runs
// out of buffer. Reports the index of `]`, whether every byte between
// `[` and `]` was an ASCII digit, and whether at least one digit
// existed (so `[]` can be rejected even though it has no non-digits).
//
// `exhausted` distinguishes "this can't be a marker — too many chars
// without `]`" (caller should flush `[` as text) from "input ended
// mid-candidate" (caller should buffer for the next Feed).
func scanMarker(buf []byte, start int) (closeIdx int, allDigits, hasDigits, exhausted bool) {
	const maxMarkerLen = 8 // protect against pathological inputs
	allDigits = true
	for i := start; i < len(buf); i++ {
		b := buf[i]
		if b == ']' {
			return i, allDigits, hasDigits, false
		}
		if b < '0' || b > '9' {
			allDigits = false
		} else {
			hasDigits = true
		}
		if i-start >= maxMarkerLen {
			return -1, false, false, true
		}
	}
	return -1, false, false, false
}
