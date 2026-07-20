package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/muty/nexus/internal/model"
	"github.com/spf13/cobra"
)

func newAskCmd(rf *rootFlags) *cobra.Command {
	var (
		modelFlag   string
		showSources bool
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask a grounded question and stream the answer",
		Long: "Ask a question against your corpus and stream the grounded answer.\n\n" +
			"Runs as a one-shot: it creates a temporary chat, streams the answer, then\n" +
			"deletes the chat (so it doesn't clutter your history). Cited sources print as\n" +
			"a footer; use --sources to show everything retrieved.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			return runAsk(cmd.Context(), client, cmd.OutOrStdout(), cmd.ErrOrStderr(), askOptions{
				question:    strings.Join(args, " "),
				model:       modelFlag,
				showSources: showSources,
				jsonOut:     jsonOut,
			})
		},
	}
	cmd.Flags().StringVarP(&modelFlag, "model", "m", "",
		"model to use (provider:id, e.g. anthropic:claude-sonnet-4-6); server default if empty")
	cmd.Flags().BoolVar(&showSources, "sources", false, "show all retrieved sources, not just cited ones")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output the full answer + sources as one JSON object")
	return cmd
}

type askOptions struct {
	question    string
	model       string
	showSources bool
	jsonOut     bool
}

// runAsk drives the ephemeral create → stream → delete lifecycle. The chat is
// always torn down — on success, on stream error, and on Ctrl-C.
func runAsk(ctx context.Context, client *cliclient.Client, out, errOut io.Writer, opts askOptions) error {
	// Convert SIGINT/SIGTERM to a ctx cancellation up front, so the whole flow
	// is graceful — there's no window where a signal terminates the process
	// before the cleanup delete is registered. SIGTERM matters for scripted use
	// (timeout(1)/kill), which would otherwise leak the ephemeral chat.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	chat, err := client.CreateChat(ctx)
	if err != nil {
		return fmt.Errorf("create chat: %w", err)
	}
	chatID := chat.ID.String()

	// Always clean up, using a fresh context so a cancelled parent (Ctrl-C)
	// doesn't also cancel the cleanup DELETE.
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if derr := client.DeleteChat(delCtx, chatID); derr != nil {
			fprintf(errOut, "warning: failed to delete temporary chat %s: %v\n", chatID, derr)
		}
	}()

	r := newAskRenderer(out, errOut, opts)
	streamErr := client.StreamMessage(ctx, chatID, opts.question, opts.model, r.handle)
	return r.finish(streamErr)
}

// askRenderer consumes the SSE frames: it streams the answer text live, collects
// the evidence union + which docs were cited, and prints a sources footer.
type askRenderer struct {
	opts   askOptions
	out    io.Writer
	errOut io.Writer

	evidence []model.ChunkPreview // union, ordered by first appearance
	seen     map[string]bool      // DocID present in evidence
	cited    map[string]bool      // DocID referenced by a citation
	answer   strings.Builder      // accumulated (for --json + the final newline)
	streamed bool                 // any text printed live

	usage      *model.ChatUsage
	stopReason string
	durationMs int
	errMsg     string
}

func newAskRenderer(out, errOut io.Writer, opts askOptions) *askRenderer {
	return &askRenderer{out: out, errOut: errOut, opts: opts, seen: map[string]bool{}, cited: map[string]bool{}}
}

// handle is the SSEHandler: it never errors (each frame is best-effort), so the
// stream runs to its natural end (the `done` frame, then EOF).
func (r *askRenderer) handle(event string, data []byte) error {
	switch event {
	case "text":
		r.onText(data)
	case "evidence", "tool_result":
		r.collectChunks(data)
	case "citation":
		r.onCitation(data)
	case "usage":
		r.onUsage(data)
	case "done":
		r.onDone(data)
	case "error":
		r.onError(data)
	}
	return nil
}

func (r *askRenderer) onText(data []byte) {
	var f struct {
		Delta string `json:"delta"`
	}
	if json.Unmarshal(data, &f) == nil && f.Delta != "" {
		r.answer.WriteString(f.Delta)
		if !r.opts.jsonOut {
			r.streamed = true
			fprintf(r.out, "%s", f.Delta)
		}
	}
}

func (r *askRenderer) collectChunks(data []byte) {
	var f struct {
		Chunks []model.ChunkPreview `json:"chunks"`
	}
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for _, ch := range f.Chunks {
		if ch.DocID == "" || r.seen[ch.DocID] {
			continue
		}
		r.seen[ch.DocID] = true
		r.evidence = append(r.evidence, ch)
	}
}

func (r *askRenderer) onCitation(data []byte) {
	var f struct {
		DocID string `json:"doc_id"`
	}
	if json.Unmarshal(data, &f) == nil && f.DocID != "" {
		r.cited[f.DocID] = true
	}
}

func (r *askRenderer) onUsage(data []byte) {
	var u model.ChatUsage
	if json.Unmarshal(data, &u) == nil {
		r.usage = &u
	}
}

func (r *askRenderer) onDone(data []byte) {
	var f struct {
		StopReason string `json:"stop_reason"`
		DurationMs int    `json:"duration_ms"`
	}
	if json.Unmarshal(data, &f) == nil {
		r.stopReason = f.StopReason
		r.durationMs = f.DurationMs
	}
}

func (r *askRenderer) onError(data []byte) {
	var f struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &f) == nil {
		r.errMsg = f.Message
	}
}

// finish prints the trailing newline + sources footer (human mode) or the JSON
// object (--json), then maps the stream outcome to an exit error.
func (r *askRenderer) finish(streamErr error) error {
	if r.opts.jsonOut {
		return r.finishJSON(streamErr)
	}
	if r.streamed {
		fprintf(r.out, "\n")
	}
	r.printSources()
	r.printMeta()
	return r.resultError(streamErr)
}

func (r *askRenderer) printSources() {
	sources := r.sourcesToShow()
	if len(sources) == 0 {
		return
	}
	fprintf(r.out, "\nSources:\n")
	for i := range sources {
		ch := &sources[i]
		label := strings.TrimSpace(ch.Title)
		if label == "" {
			label = "(untitled)"
		}
		meta := ch.SourceName
		if meta == "" {
			meta = ch.Source
		}
		line := fmt.Sprintf("  [%d] %s", i+1, label)
		if meta != "" {
			line += " — " + meta
		}
		if ch.Date != "" {
			line += " (" + ch.Date + ")"
		}
		fprintf(r.out, "%s\n", line)
		if ch.URL != "" {
			fprintf(r.out, "      %s\n", ch.URL)
		}
	}
}

// sourcesToShow returns the cited evidence by default, falling back to the full
// retrieved union when the answer cited nothing (so a grounded turn that emitted
// no citation frames doesn't read as ungrounded). --sources always shows the
// full union.
func (r *askRenderer) sourcesToShow() []model.ChunkPreview {
	if !r.opts.showSources {
		cited := make([]model.ChunkPreview, 0, len(r.evidence))
		for i := range r.evidence {
			if r.cited[r.evidence[i].DocID] {
				cited = append(cited, r.evidence[i])
			}
		}
		if len(cited) > 0 {
			return cited
		}
	}
	return r.evidence
}

func (r *askRenderer) printMeta() {
	var parts []string
	if r.usage != nil {
		parts = append(parts, fmt.Sprintf("tokens in=%d out=%d", r.usage.Input, r.usage.Output))
	}
	if r.durationMs > 0 {
		parts = append(parts, fmt.Sprintf("%dms", r.durationMs))
	}
	if len(parts) > 0 {
		fprintf(r.errOut, "(%s)\n", strings.Join(parts, ", "))
	}
}

func (r *askRenderer) finishJSON(streamErr error) error {
	type jsonSource struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Source     string `json:"source"`
		SourceName string `json:"source_name,omitempty"`
		Date       string `json:"date,omitempty"`
		URL        string `json:"url,omitempty"`
		Cited      bool   `json:"cited"`
	}
	sources := make([]jsonSource, 0, len(r.evidence))
	for i := range r.evidence {
		ch := &r.evidence[i]
		sources = append(sources, jsonSource{
			ID: ch.DocID, Title: ch.Title, Source: ch.Source, SourceName: ch.SourceName,
			Date: ch.Date, URL: ch.URL, Cited: r.cited[ch.DocID],
		})
	}
	// Fold a pre-stream/transport failure into the payload so the JSON object on
	// stdout is self-describing (an `error` SSE frame already set r.errMsg). A
	// graceful Ctrl-C (context.Canceled) is not a failure.
	errStr := r.errMsg
	if errStr == "" && streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		errStr = streamErr.Error()
	}
	payload := struct {
		Answer     string           `json:"answer"`
		Sources    []jsonSource     `json:"sources"`
		Usage      *model.ChatUsage `json:"usage,omitempty"`
		StopReason string           `json:"stop_reason,omitempty"`
		DurationMs int              `json:"duration_ms,omitempty"`
		Error      string           `json:"error,omitempty"`
	}{
		Answer: r.answer.String(), Sources: sources, Usage: r.usage,
		StopReason: r.stopReason, DurationMs: r.durationMs, Error: errStr,
	}
	if err := writeJSON(r.out, payload); err != nil {
		return err
	}
	return r.resultError(streamErr)
}

// resultError maps the stream outcome to a command exit error. An `error` frame
// wins; a Ctrl-C (context canceled) is a graceful stop, not a failure.
func (r *askRenderer) resultError(streamErr error) error {
	switch {
	case r.errMsg != "":
		return errors.New(r.errMsg)
	case streamErr != nil && errors.Is(streamErr, context.Canceled):
		return nil
	case streamErr != nil:
		return streamErr
	case r.stopReason == "error":
		return errors.New("answer ended with an error")
	default:
		return nil
	}
}
