package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// fakeSearchClient implements the subset of the search client used by
// drainErr via its error channel semantics.
//
// Kept deliberately minimal: mergeDiffStaleIDs / drainOSErrsOrReturn
// operate on bare channels, so we don't need a full mock.

// TestHandleItem_ScopeUpdatesSeenByProgress covers the Scope branch
// of handleItem: scope updates fire an immediate progress callback
// with the new label so the UI can show "Syncing Archive…" the
// moment the connector enters a new folder/chat.
func TestHandleItem_ScopeUpdatesSeenByProgress(t *testing.T) {
	p := &Pipeline{log: zap.NewNop()}
	var gotScope string
	state := &runState{
		progress: func(_, _, _ int, scope string) { gotScope = scope },
	}
	scope := "Archive"
	p.handleItem(context.Background(), state, model.FetchItem{Scope: &scope})
	if gotScope != "Archive" {
		t.Errorf("scope = %q, want Archive", gotScope)
	}
	if state.scope != "Archive" {
		t.Errorf("state.scope = %q, want Archive", state.scope)
	}
}

// TestHandleItem_EstimatedTotalGrowsButNeverShrinks: the SSE contract
// says total may grow across frames (IMAP reports per-folder), but
// we never let a smaller estimate regress the value.
func TestHandleItem_EstimatedTotalGrowsButNeverShrinks(t *testing.T) {
	p := &Pipeline{log: zap.NewNop()}
	state := &runState{}
	hundred := int64(100)
	fifty := int64(50)
	p.handleItem(context.Background(), state, model.FetchItem{EstimatedTotal: &hundred})
	p.handleItem(context.Background(), state, model.FetchItem{EstimatedTotal: &fifty})
	if state.total != 100 {
		t.Errorf("state.total = %d, want 100 (estimates only grow)", state.total)
	}
	if !state.hasEstimate {
		t.Error("hasEstimate should be true after any EstimatedTotal emission")
	}
}

// TestHandleItem_EnumerationCompleteSetsFlag — the terminal
// "connector opted into reconciliation" marker must set
// sawEnumeration even if no SourceIDs were emitted (empty source
// "wipe all" case).
func TestHandleItem_EnumerationCompleteSetsFlag(t *testing.T) {
	p := &Pipeline{log: zap.NewNop()}
	state := &runState{}
	p.handleItem(context.Background(), state, model.FetchItem{EnumerationComplete: true})
	if !state.sawEnumeration {
		t.Error("EnumerationComplete must flip sawEnumeration")
	}
}

// TestDrainOSErrsOrReturn_CleanClose covers the happy path —
// a closed errs channel with no pending error returns the stale slice
// as-is.
func TestDrainOSErrsOrReturn_CleanClose(t *testing.T) {
	errs := make(chan error)
	close(errs)
	stale := []string{"a", "b"}
	got := drainOSErrsOrReturn(errs, stale)
	if !reflect.DeepEqual(got, stale) {
		t.Errorf("expected stale unchanged, got %v", got)
	}
}

// TestDrainOSErrsOrReturn_ErrorReturnsNil covers the error-draining
// path: any non-nil error on the channel signals "don't flush
// deletions" and the function returns nil as the sentinel.
func TestDrainOSErrsOrReturn_ErrorReturnsNil(t *testing.T) {
	errs := make(chan error, 1)
	errs <- errors.New("boom")
	close(errs)
	got := drainOSErrsOrReturn(errs, []string{"a"})
	if got != nil {
		t.Errorf("expected nil on error, got %v", got)
	}
}

// TestDrainOSErrsOrReturn_NilErrorIgnored proves a nil-valued error
// on the channel doesn't poison the result — only real errors do.
func TestDrainOSErrsOrReturn_NilErrorIgnored(t *testing.T) {
	errs := make(chan error, 1)
	errs <- nil
	close(errs)
	stale := []string{"kept"}
	got := drainOSErrsOrReturn(errs, stale)
	if !reflect.DeepEqual(got, stale) {
		t.Errorf("nil errors must be ignored, got %v", got)
	}
}

// Enforce module-level usage of time package so unused-import lint
// doesn't fire if future coverage tests drop their time references.
var _ = time.Now

// streamChans is a tiny test helper for constructing the (items,
// errs) channel pair mergeDiffStaleIDs expects. Pre-populates both
// channels with the provided items/errors and closes them — since
// mergeDiffStaleIDs reads from both, this lets us drive deterministic
// scenarios without a goroutine per test.
func streamChans(items []string, finalErr error) (<-chan string, <-chan error) {
	itemsCh := make(chan string, len(items))
	errsCh := make(chan error, 1)
	for _, s := range items {
		itemsCh <- s
	}
	close(itemsCh)
	if finalErr != nil {
		errsCh <- finalErr
	}
	close(errsCh)
	return itemsCh, errsCh
}

func TestMergeDiffStaleIDs_AllKeep(t *testing.T) {
	connectorIDs := []string{"a.txt", "b.txt", "c.txt"}
	items, errs := streamChans([]string{"a.txt", "b.txt", "c.txt"}, nil)
	stale := mergeDiffStaleIDs(context.Background(), connectorIDs, items, errs)
	if len(stale) != 0 {
		t.Errorf("no deletions expected, got %v", stale)
	}
}

func TestMergeDiffStaleIDs_IndexedOnlyEntriesAreStale(t *testing.T) {
	// Connector says a and c still exist; OpenSearch has a, b, c, d.
	// The merge-diff should flag b and d as stale.
	connectorIDs := []string{"a", "c"}
	items, errs := streamChans([]string{"a", "b", "c", "d"}, nil)
	stale := mergeDiffStaleIDs(context.Background(), connectorIDs, items, errs)
	want := []string{"b", "d"}
	if !reflect.DeepEqual(stale, want) {
		t.Errorf("stale = %v, want %v", stale, want)
	}
}

func TestMergeDiffStaleIDs_PreservesColonSuffixChildren(t *testing.T) {
	// IMAP emits parent UIDs only; attachments carry colon-suffix
	// source_ids. Indexed `INBOX:42:attachment:0` must NOT be
	// deleted when the parent `INBOX:42` is still in the keep set,
	// even though the child doesn't appear in connectorIDs.
	connectorIDs := []string{"INBOX:42"}
	items, errs := streamChans(
		[]string{"INBOX:42", "INBOX:42:attachment:0", "INBOX:42:attachment:1"},
		nil,
	)
	stale := mergeDiffStaleIDs(context.Background(), connectorIDs, items, errs)
	if len(stale) != 0 {
		t.Errorf("children should be preserved, got stale=%v", stale)
	}
}

func TestMergeDiffStaleIDs_DeletesOrphanChildren(t *testing.T) {
	// An indexed child whose parent is NOT in the keep set is a
	// true orphan and must be deleted.
	connectorIDs := []string{"INBOX:42"}
	items, errs := streamChans(
		[]string{"INBOX:42", "INBOX:99:attachment:0"}, nil,
	)
	stale := mergeDiffStaleIDs(context.Background(), connectorIDs, items, errs)
	if len(stale) != 1 || stale[0] != "INBOX:99:attachment:0" {
		t.Errorf("expected orphan deletion, got %v", stale)
	}
}

func TestMergeDiffStaleIDs_StreamErrorReturnsNil(t *testing.T) {
	// A mid-stream error must return nil (sentinel) so the caller
	// doesn't flush a partial delete list — we'd otherwise nuke ids
	// we never saw from OpenSearch.
	items, errs := streamChans([]string{"a"}, errors.New("boom"))
	stale := mergeDiffStaleIDs(context.Background(), []string{"a"}, items, errs)
	if stale != nil {
		t.Errorf("expected nil on stream error, got %v", stale)
	}
}

func TestMergeDiffStaleIDs_ContextCancelReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Use a blocking, never-close items channel so the only signal
	// available is ctx.Done.
	items := make(chan string)
	errs := make(chan error)
	stale := mergeDiffStaleIDs(ctx, nil, items, errs)
	if stale != nil {
		t.Errorf("expected nil on ctx cancel, got %v", stale)
	}
}

func TestMergeDiffStaleIDs_EmptyIndexedNothingStale(t *testing.T) {
	// OpenSearch has no docs for this connector (first-ever sync
	// after a clean index recreate). Nothing to delete regardless
	// of what the connector enumerates.
	items, errs := streamChans(nil, nil)
	stale := mergeDiffStaleIDs(context.Background(), []string{"a", "b"}, items, errs)
	if len(stale) != 0 {
		t.Errorf("expected no stale when index is empty, got %v", stale)
	}
}

func TestMergeDiffStaleIDs_EmptyConnectorListEverythingStale(t *testing.T) {
	// Connector claims nothing exists; every indexed id should be
	// flagged stale (the "empty wipes all" case, gated on the
	// pipeline having seen EnumerationComplete).
	items, errs := streamChans([]string{"a.txt", "b.txt"}, nil)
	stale := mergeDiffStaleIDs(context.Background(), nil, items, errs)
	want := []string{"a.txt", "b.txt"}
	if !reflect.DeepEqual(stale, want) {
		t.Errorf("stale = %v, want %v", stale, want)
	}
}
