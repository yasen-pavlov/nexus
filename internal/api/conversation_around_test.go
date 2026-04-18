//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func seedMessagesAt(t *testing.T, sc searchIndexer, ownerID, conversation string, at []time.Time) {
	t.Helper()
	chunks := make([]model.Chunk, len(at))
	for i, ts := range at {
		sid := fmt.Sprintf("%s:%d:msg", conversation, i+1)
		chunks[i] = model.Chunk{
			ID:             sid + ":0",
			ParentID:       sid,
			DocID:          sid, // collapse key; must be unique per message
			ChunkIndex:     0,
			Title:          "msg",
			Content:        fmt.Sprintf("message %d", i+1),
			FullContent:    fmt.Sprintf("message %d", i+1),
			SourceType:     "telegram",
			SourceName:     "tg",
			SourceID:       sid,
			ConversationID: conversation,
			Hidden:         true,
			Visibility:     "private",
			OwnerID:        ownerID,
			CreatedAt:      ts,
		}
	}
	if err := sc.IndexChunks(context.Background(), chunks); err != nil {
		t.Fatalf("seed chunks: %v", err)
	}
	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestConversationAround_CentersOnAnchor(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	// 10 messages evenly spaced. Anchor at the 5th.
	base := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	ts := make([]time.Time, 10)
	for i := range ts {
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}
	seedMessagesAt(t, sc, userID.String(), "conv-1", ts)

	// limit=4 → 2 before (inclusive of anchor) + 2 after
	anchor := ts[4].Format(time.RFC3339)
	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/conv-1/messages?around="+anchor+"&limit=4",
		"", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out struct {
		Messages   []model.Document `json:"messages"`
		NextBefore *time.Time       `json:"next_before"`
		NextAfter  *time.Time       `json:"next_after"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(out.Messages) != 4 {
		t.Fatalf("expected 4 messages centered on anchor, got %d", len(out.Messages))
	}
	// Messages should be sorted ASC.
	for i := 1; i < len(out.Messages); i++ {
		if out.Messages[i].CreatedAt.Before(out.Messages[i-1].CreatedAt) {
			t.Errorf("messages not sorted ASC at index %d", i)
		}
	}
	// The anchor (ts[4]) should be present.
	foundAnchor := false
	for _, m := range out.Messages {
		if m.CreatedAt.Equal(ts[4]) {
			foundAnchor = true
			break
		}
	}
	if !foundAnchor {
		t.Errorf("anchor timestamp not found in response")
	}
	// Both cursors present — older & newer messages exist.
	if out.NextBefore == nil {
		t.Errorf("expected next_before (older messages exist)")
	}
	if out.NextAfter == nil {
		t.Errorf("expected next_after (newer messages exist)")
	}
}

func TestConversationAround_NoCursorsWhenSmallConversation(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	base := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	ts := []time.Time{base, base.Add(time.Minute), base.Add(2 * time.Minute)}
	seedMessagesAt(t, sc, userID.String(), "conv-small", ts)

	anchor := ts[1].Format(time.RFC3339)
	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/conv-small/messages?around="+anchor+"&limit=50",
		"", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out struct {
		Messages   []model.Document `json:"messages"`
		NextBefore *time.Time       `json:"next_before"`
		NextAfter  *time.Time       `json:"next_after"`
	}
	_ = json.Unmarshal(data, &out)
	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 messages total, got %d", len(out.Messages))
	}
	if out.NextBefore != nil {
		t.Errorf("next_before should be nil when nothing older")
	}
	if out.NextAfter != nil {
		t.Errorf("next_after should be nil when nothing newer")
	}
}

func TestConversation_BeforeCursorEmitsNextBefore(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	base := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	ts := make([]time.Time, 8)
	for i := range ts {
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}
	seedMessagesAt(t, sc, userID.String(), "conv-b", ts)

	before := ts[6].Format(time.RFC3339)
	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/conv-b/messages?before="+before+"&limit=3",
		"", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out struct {
		Messages   []model.Document `json:"messages"`
		NextBefore *time.Time       `json:"next_before"`
		NextAfter  *time.Time       `json:"next_after"`
	}
	_ = json.Unmarshal(data, &out)

	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out.Messages))
	}
	// Before-direction full page → next_before emitted, next_after not.
	if out.NextBefore == nil {
		t.Errorf("expected next_before for a full before-page")
	}
	if out.NextAfter != nil {
		t.Errorf("next_after should not be set on before-direction queries")
	}
}

func TestConversation_AfterCursorEmitsNextAfter(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	base := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	ts := make([]time.Time, 8)
	for i := range ts {
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}
	seedMessagesAt(t, sc, userID.String(), "conv-a", ts)

	after := ts[1].Format(time.RFC3339)
	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/conv-a/messages?after="+after+"&limit=3",
		"", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out struct {
		Messages   []model.Document `json:"messages"`
		NextBefore *time.Time       `json:"next_before"`
		NextAfter  *time.Time       `json:"next_after"`
	}
	_ = json.Unmarshal(data, &out)

	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out.Messages))
	}
	if out.NextAfter == nil {
		t.Errorf("expected next_after for a full after-page")
	}
	if out.NextBefore != nil {
		t.Errorf("next_before should not be set on after-direction queries")
	}
}

func TestConversation_TailOpenEmitsNextBeforeOnly(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	base := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	ts := make([]time.Time, 8)
	for i := range ts {
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}
	seedMessagesAt(t, sc, userID.String(), "conv-t", ts)

	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/conv-t/messages?limit=3",
		"", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out struct {
		Messages   []model.Document `json:"messages"`
		NextBefore *time.Time       `json:"next_before"`
		NextAfter  *time.Time       `json:"next_after"`
	}
	_ = json.Unmarshal(data, &out)

	// Tail mode returns the N most recent messages, no "next_after"
	// since the caller already holds the latest.
	if out.NextBefore == nil {
		t.Errorf("tail open with more history should emit next_before")
	}
	if out.NextAfter != nil {
		t.Errorf("tail open should never emit next_after")
	}
}

func TestConversation_RejectsInvalidBefore(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)
	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/c/messages?before=not-a-time",
		"", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad 'before', got %d", w.Code)
	}
}

func TestConversation_RejectsInvalidAfter(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)
	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/c/messages?after=not-a-time",
		"", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad 'after', got %d", w.Code)
	}
}

func TestConversation_RejectsInvalidAroundOrLimit(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)

	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/c/messages?around=nope",
		"", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad 'around', got %d", w.Code)
	}

	w = doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/c/messages?limit=-1",
		"", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad 'limit', got %d", w.Code)
	}
}

func TestConversationAround_RejectsCombinedCursors(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)

	w := doJSON(t, router, http.MethodGet,
		"/api/conversations/telegram/c/messages?around=2026-04-10T10:00:00Z&before=2026-04-10T12:00:00Z",
		"", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
