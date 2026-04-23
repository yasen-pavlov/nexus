package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
)

// StreamResult is the aggregate view of everything a connector's
// streaming Fetch emitted during a single run. Tests use this instead
// of re-implementing the channel-drain loop each time, preserving the
// feel of the old slice-based FetchResult assertions while running
// the real streaming code path.
type StreamResult struct {
	Documents       []model.Document
	SourceIDs       []string
	Checkpoints     []*model.SyncCursor
	EstimatedTotals []int64
	// LastCursor is the last Checkpoint observed on the stream (or nil
	// if the connector never emitted one). This is the cursor the
	// pipeline would have persisted for the run.
	LastCursor *model.SyncCursor
	// Err is the terminal error reported on the errs channel (nil on
	// clean completion).
	Err error
}

// CollectStream drains a streaming Fetch into a StreamResult. It
// blocks until both channels close, so callers should ensure the
// connector will terminate (e.g. bounded test data or a cancellable
// context). The default timeout is generous enough for slow tests
// but bounded so a hung connector fails fast.
func CollectStream(t testing.TB, items <-chan model.FetchItem, errs <-chan error) *StreamResult {
	t.Helper()
	return CollectStreamWithTimeout(t, items, errs, 30*time.Second)
}

// CollectStreamWithTimeout is CollectStream with a custom overall
// timeout. Useful for tests that need a shorter deadline (e.g.
// cancellation tests) or a longer one (integration tests touching
// real external services).
func CollectStreamWithTimeout(t testing.TB, items <-chan model.FetchItem, errs <-chan error, timeout time.Duration) *StreamResult {
	t.Helper()
	out := &StreamResult{}
	deadline := time.After(timeout)
	itemsDone := false
	errsDone := false
	for !itemsDone || !errsDone {
		select {
		case item, ok := <-items:
			if !ok {
				itemsDone = true
				continue
			}
			switch {
			case item.Doc != nil:
				out.Documents = append(out.Documents, *item.Doc)
			case item.SourceID != nil:
				out.SourceIDs = append(out.SourceIDs, *item.SourceID)
			case item.Checkpoint != nil:
				out.Checkpoints = append(out.Checkpoints, item.Checkpoint)
				out.LastCursor = item.Checkpoint
			case item.EstimatedTotal != nil:
				out.EstimatedTotals = append(out.EstimatedTotals, *item.EstimatedTotal)
			}
		case err, ok := <-errs:
			if !ok {
				errsDone = true
				continue
			}
			if err != nil && out.Err == nil {
				out.Err = err
			}
		case <-deadline:
			t.Fatalf("CollectStream: timeout after %s", timeout)
			return out
		}
	}
	return out
}

// RunFetch calls conn.Fetch with a fresh background context and
// collects the resulting stream. Convenience wrapper for tests that
// don't need to control the context.
func RunFetch(t testing.TB, conn connector.Connector, cursor *model.SyncCursor) *StreamResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, errs := conn.Fetch(ctx, cursor)
	return CollectStream(t, items, errs)
}

// StreamOf turns a fixed slice of FetchItems into a pair of channels
// shaped like a connector's Fetch return. Used by tests that need to
// inject a canned stream into the pipeline without writing a full
// fake connector. Items are emitted in order and the errs channel
// closes cleanly after the items channel.
func StreamOf(items ...model.FetchItem) (<-chan model.FetchItem, <-chan error) {
	ch := make(chan model.FetchItem, len(items))
	errs := make(chan error, 1)
	for _, it := range items {
		ch <- it
	}
	close(ch)
	close(errs)
	return ch, errs
}

// DocItems wraps each doc in a FetchItem{Doc:...}. Handy for
// building test fixtures without a FetchItem literal per doc.
func DocItems(docs ...model.Document) []model.FetchItem {
	out := make([]model.FetchItem, len(docs))
	for i := range docs {
		d := docs[i]
		out[i] = model.FetchItem{Doc: &d}
	}
	return out
}

// SourceIDItems wraps each id in a FetchItem{SourceID:...}.
func SourceIDItems(ids ...string) []model.FetchItem {
	out := make([]model.FetchItem, len(ids))
	for i := range ids {
		id := ids[i]
		out[i] = model.FetchItem{SourceID: &id}
	}
	return out
}

// CheckpointItem returns a FetchItem wrapping a cursor checkpoint.
func CheckpointItem(c *model.SyncCursor) model.FetchItem {
	return model.FetchItem{Checkpoint: c}
}
