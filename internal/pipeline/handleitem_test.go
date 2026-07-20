package pipeline

import (
	"context"
	"fmt"
	"testing"

	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// TestHandleItem_ErrBranch covers the non-fatal per-unit error path: a
// FetchItem carrying an Err (e.g. one Telegram chat's pagination failed) must
// bump the run's error count and fire a progress update, so a run with
// silently-failed sub-units isn't reported as a clean success.
func TestHandleItem_ErrBranch(t *testing.T) {
	p := New(nil, nil, nil, zap.NewNop())

	var lastErrCount int
	var progressCalls int
	state := &runState{
		connName: "tg",
		connType: "telegram",
		progress: func(_, _, errCount int, _ string) {
			progressCalls++
			lastErrCount = errCount
		},
	}

	p.handleItem(context.Background(), state, model.FetchItem{Err: fmt.Errorf("CHANNEL_PRIVATE")})

	if state.errCount != 1 {
		t.Errorf("errCount = %d, want 1", state.errCount)
	}
	if progressCalls != 1 {
		t.Errorf("progress calls = %d, want 1", progressCalls)
	}
	if lastErrCount != 1 {
		t.Errorf("progress reported errCount = %d, want 1", lastErrCount)
	}

	// A second per-unit error accumulates.
	p.handleItem(context.Background(), state, model.FetchItem{Err: fmt.Errorf("FLOOD_WAIT")})
	if state.errCount != 2 {
		t.Errorf("errCount after second error = %d, want 2", state.errCount)
	}
}

// TestHandleItem_ErrBranch_NilProgress ensures the Err branch is safe when no
// progress callback is registered.
func TestHandleItem_ErrBranch_NilProgress(t *testing.T) {
	p := New(nil, nil, nil, zap.NewNop())
	state := &runState{connName: "tg", connType: "telegram"}
	p.handleItem(context.Background(), state, model.FetchItem{Err: fmt.Errorf("boom")})
	if state.errCount != 1 {
		t.Errorf("errCount = %d, want 1", state.errCount)
	}
}
