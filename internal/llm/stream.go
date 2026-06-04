package llm

import (
	"context"
	"fmt"
	"strings"
)

// SendOrCancel sends ev on out, or aborts if ctx is cancelled. Returns true if
// the event was delivered, false if the context was done first. Shared by every
// provider adapter so cancellation semantics stay identical across them.
//
// On cancellation it makes a best-effort, NON-BLOCKING attempt to deliver a
// final cancelled EventDone. The non-blocking select matters: the consumer
// (the RAG orchestrator) reacts to ctx cancellation on its own and stops
// draining, so a blocking send here would leak the adapter goroutine forever
// when the channel buffer is full. Dropping the redundant frame is safe.
func SendOrCancel(ctx context.Context, out chan<- Event, ev Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		select {
		case out <- Event{Kind: EventDone, StopReason: StopCancelled}:
		default:
		}
		return false
	}
}

// RenderDocumentsBlock formats retrieved documents as the pseudo-XML block fed
// to providers without native document support (OpenAI, Ollama) as a synthetic
// retrieved-context turn. The [N] index lets the model cite documents; the
// CitationParser lifts those markers back out of the response.
//
// Every attribute value is HTML-escaped so a document title/source containing
// a quote or angle bracket cannot break out of the attribute and corrupt the
// block. Content sits between the tags and is emitted verbatim.
func RenderDocumentsBlock(docs []Document) string {
	var b strings.Builder
	b.WriteString("Retrieved documents (cite as [N] where N is the document number):\n")
	for i, d := range docs {
		fmt.Fprintf(&b, "\n<document index=\"%d\" id=\"%s\"", i+1, escapeAttr(d.ID))
		if d.Source != "" {
			fmt.Fprintf(&b, " source=\"%s\"", escapeAttr(d.Source))
		}
		if d.Title != "" {
			fmt.Fprintf(&b, " title=\"%s\"", escapeAttr(d.Title))
		}
		if d.Date != "" {
			fmt.Fprintf(&b, " date=\"%s\"", escapeAttr(d.Date))
		}
		b.WriteString(">\n")
		b.WriteString(d.Content)
		b.WriteString("\n</document>\n")
	}
	return b.String()
}

// escapeAttr HTML-escapes a value destined for a double-quoted XML attribute.
func escapeAttr(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, "&quot;"...)
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		case '&':
			out = append(out, "&amp;"...)
		default:
			out = append(out, string(r)...)
		}
	}
	return string(out)
}
