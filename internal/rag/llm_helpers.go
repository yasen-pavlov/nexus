package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/muty/nexus/internal/llm"
)

// collectText runs a one-shot llm.Generate call and drains the streamed
// channel into a single accumulated text + the final Usage. Used by the
// Phase 4 rewriter and title-summariser, both of which need a "fire one
// short prompt, get the full answer back" shape on top of the existing
// streaming Generator interface — there's no need to add a separate
// Complete() method to the interface for these short cheap-model calls.
//
// Honours ctx cancellation (returns ctx.Err()), surfaces an EventError
// from the stream as an error, and treats a channel-close-without-Done
// as a partial success (returns whatever text we accumulated, no error,
// usage may be nil).
func collectText(ctx context.Context, gen llm.Generator, req llm.GenerateRequest) (string, *llm.Usage, error) {
	if gen == nil {
		return "", nil, errors.New("rag: collectText: nil generator")
	}
	events, err := gen.Generate(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("rag: collectText: generate: %w", err)
	}

	var (
		buf   strings.Builder
		usage *llm.Usage
	)
	for {
		select {
		case <-ctx.Done():
			return buf.String(), usage, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return buf.String(), usage, nil
			}
			switch ev.Kind {
			case llm.EventText:
				buf.WriteString(ev.TextDelta)
			case llm.EventDone:
				if ev.Usage != nil {
					usage = ev.Usage
				}
				return buf.String(), usage, nil
			case llm.EventError:
				if ev.Err != nil {
					return buf.String(), usage, ev.Err
				}
				return buf.String(), usage, errors.New("rag: collectText: stream error")
			}
		}
	}
}
