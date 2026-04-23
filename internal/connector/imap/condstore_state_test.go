package imap

import (
	"context"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/muty/nexus/internal/model"
)

// newTestContextDone returns a context that's already been
// cancelled, plus its cancel function for defer.
func newTestContextDone() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// TestBuildEmailRelations_ReplyAndThreadRootDiffer covers the
// primary case: InReplyTo names the direct parent, but a
// References header names a different (earlier) message that
// everyone in the thread points at.
func TestBuildEmailRelations_ReplyAndThreadRootDiffer(t *testing.T) {
	env := &imap.Envelope{
		InReplyTo: []string{"<parent@x.com>"},
	}
	body := []byte("References: <root@x.com> <mid@x.com> <parent@x.com>\r\n\r\nbody")
	rels := buildEmailRelations(env, body)
	if len(rels) != 2 {
		t.Fatalf("want 2 relations, got %d: %+v", len(rels), rels)
	}
	if rels[0].Type != model.RelationReplyTo || rels[0].TargetSourceID != "parent@x.com" {
		t.Errorf("replyTo wrong: %+v", rels[0])
	}
	if rels[1].Type != model.RelationMemberOfThread || rels[1].TargetSourceID != "root@x.com" {
		t.Errorf("thread-root wrong: %+v", rels[1])
	}
}

// TestBuildEmailRelations_NoReferencesFallsBackToInReplyTo
// covers the branch where no References header is present —
// InReplyTo doubles as the thread root (single-reply thread).
func TestBuildEmailRelations_NoReferencesFallsBackToInReplyTo(t *testing.T) {
	env := &imap.Envelope{
		InReplyTo: []string{"<parent@x.com>"},
	}
	rels := buildEmailRelations(env, []byte(""))
	if len(rels) != 2 {
		t.Fatalf("want 2 relations, got %d", len(rels))
	}
	if rels[1].TargetSourceID != "parent@x.com" {
		t.Errorf("thread-root should fall back to InReplyTo, got %q", rels[1].TargetSourceID)
	}
}

// TestBuildEmailRelations_NoInReplyToNoReferences covers the
// top-of-thread case: the email doesn't reply to anything and has
// no References header. Zero relations.
func TestBuildEmailRelations_NoInReplyToNoReferences(t *testing.T) {
	env := &imap.Envelope{}
	rels := buildEmailRelations(env, nil)
	if len(rels) != 0 {
		t.Errorf("top-of-thread email should have no relations, got %+v", rels)
	}
}

// TestBuildEmailRelations_ReferencesButNoInReplyTo covers the
// "forwarded" case: the References header threads the email even
// though there's no direct reply — only member-of-thread fires.
func TestBuildEmailRelations_ReferencesButNoInReplyTo(t *testing.T) {
	env := &imap.Envelope{}
	body := []byte("References: <root@x.com>\r\n\r\nbody")
	rels := buildEmailRelations(env, body)
	if len(rels) != 1 || rels[0].Type != model.RelationMemberOfThread {
		t.Errorf("expected only member-of-thread, got %+v", rels)
	}
}

// TestResolveCondStoreState_UIDValidityRotationInvalidatesModSeq
// covers the RFC 7162 §5 rule: when UIDVALIDITY changes, the
// cached HighestModSeq becomes meaningless because UIDs were
// re-keyed. The helper must zero cachedModSeq so the caller falls
// back to a full re-fetch.
func TestResolveCondStoreState_UIDValidityRotationInvalidatesModSeq(t *testing.T) {
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"uidvalidity:INBOX": float64(42),
			"modseq:INBOX":      float64(100),
		},
	}
	sel := &imap.SelectData{UIDValidity: 999, HighestModSeq: 200}
	st := resolveCondStoreState(cursor, "INBOX", sel)
	if st.cachedModSeq != 0 {
		t.Errorf("cachedModSeq = %d, want 0 after UIDValidity rotation", st.cachedModSeq)
	}
	if st.newUIDValidity != 999 {
		t.Errorf("newUIDValidity = %d, want 999", st.newUIDValidity)
	}
}

// TestResolveCondStoreState_CarriesCachedValues covers the
// stable-validity path where cachedModSeq must survive.
func TestResolveCondStoreState_CarriesCachedValues(t *testing.T) {
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"uidvalidity:INBOX": float64(42),
			"modseq:INBOX":      float64(100),
		},
	}
	sel := &imap.SelectData{UIDValidity: 42, HighestModSeq: 200}
	st := resolveCondStoreState(cursor, "INBOX", sel)
	if st.cachedModSeq != 100 {
		t.Errorf("cachedModSeq = %d, want 100", st.cachedModSeq)
	}
	if st.newHighestModSeq != 200 {
		t.Errorf("newHighestModSeq = %d, want 200", st.newHighestModSeq)
	}
}

// TestCondStoreState_Unchanged covers the fast-skip predicate.
func TestCondStoreState_Unchanged(t *testing.T) {
	cases := []struct {
		name string
		st   condStoreState
		want bool
	}{
		{"all match", condStoreState{cachedUIDValidity: 1, cachedModSeq: 5, newUIDValidity: 1, newHighestModSeq: 5}, true},
		{"modseq advanced", condStoreState{cachedUIDValidity: 1, cachedModSeq: 5, newUIDValidity: 1, newHighestModSeq: 6}, false},
		{"uidvalidity changed", condStoreState{cachedUIDValidity: 1, cachedModSeq: 5, newUIDValidity: 2, newHighestModSeq: 5}, false},
		{"no condstore (HighestModSeq=0)", condStoreState{cachedUIDValidity: 1, cachedModSeq: 0, newUIDValidity: 1, newHighestModSeq: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.unchanged(); got != tc.want {
				t.Errorf("unchanged() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWriteFolderCursor_IncludesModSeqWhenCondStoreAdvertised
// covers the happy path: server speaks CONDSTORE, cursor stores
// modseq.
func TestWriteFolderCursor_IncludesModSeqWhenCondStoreAdvertised(t *testing.T) {
	cursorData := map[string]any{}
	writeFolderCursor(cursorData, "INBOX",
		condStoreState{newUIDValidity: 7, newHighestModSeq: 42}, imap.UID(100))
	if v, _ := cursorData["uidvalidity:INBOX"].(float64); v != 7 {
		t.Errorf("uidvalidity = %v", v)
	}
	if v, _ := cursorData["modseq:INBOX"].(float64); v != 42 {
		t.Errorf("modseq = %v", v)
	}
	if v, _ := cursorData["uid:INBOX"].(float64); v != 100 {
		t.Errorf("uid = %v", v)
	}
}

// TestWriteFolderCursor_OmitsModSeqOnNonCondStoreServer: when
// the server reports HighestModSeq=0 we pin uidvalidity + uid
// but leave modseq absent so we don't accidentally anchor a
// fake zero value.
func TestWriteFolderCursor_OmitsModSeqOnNonCondStoreServer(t *testing.T) {
	cursorData := map[string]any{}
	writeFolderCursor(cursorData, "INBOX",
		condStoreState{newUIDValidity: 7, newHighestModSeq: 0}, imap.UID(100))
	if _, has := cursorData["modseq:INBOX"]; has {
		t.Errorf("modseq should be absent when HighestModSeq=0, got %v", cursorData["modseq:INBOX"])
	}
}

// TestBuildDeltaCriteria_CondStoreDelta covers the O(delta)
// CONDSTORE path — when both cached and server HighestModSeq are
// non-zero, we ask for "UIDs changed since cachedModSeq" and
// layer ChangedSince onto the FETCH options.
func TestBuildDeltaCriteria_CondStoreDelta(t *testing.T) {
	c := &Connector{}
	criteria, opts := c.buildDeltaCriteria(nil, "INBOX", 50, 100)
	if criteria.ModSeq == nil || criteria.ModSeq.ModSeq != 50 {
		t.Errorf("expected ModSeq=50, got %+v", criteria.ModSeq)
	}
	if opts.ChangedSince != 50 {
		t.Errorf("ChangedSince = %d, want 50", opts.ChangedSince)
	}
}

// TestBuildDeltaCriteria_FallsBackToUIDRange: when the server
// doesn't speak CONDSTORE (HighestModSeq == 0), we use the
// UID-range heuristic — UID > lastUID from the cursor.
func TestBuildDeltaCriteria_FallsBackToUIDRange(t *testing.T) {
	c := &Connector{}
	cursor := &model.SyncCursor{CursorData: map[string]any{"uid:INBOX": float64(42)}}
	criteria, _ := c.buildDeltaCriteria(cursor, "INBOX", 0, 0)
	if criteria.ModSeq != nil {
		t.Errorf("expected no ModSeq in fallback path, got %+v", criteria.ModSeq)
	}
	if len(criteria.UID) == 0 {
		t.Errorf("expected UID range criterion, got %+v", criteria)
	}
}

// TestBuildDeltaCriteria_NoCachedModSeqFallsBack: cached ModSeq=0
// (first sync with CONDSTORE server) means we can't compute a
// delta — fall back to UID-range just like the no-CONDSTORE case.
func TestBuildDeltaCriteria_NoCachedModSeqFallsBack(t *testing.T) {
	c := &Connector{}
	criteria, opts := c.buildDeltaCriteria(nil, "INBOX", 0, 200)
	if criteria.ModSeq != nil {
		t.Errorf("first sync shouldn't use MODSEQ criterion, got %+v", criteria.ModSeq)
	}
	if opts.ChangedSince != 0 {
		t.Errorf("first sync shouldn't set ChangedSince, got %d", opts.ChangedSince)
	}
}

// TestEmitItem_CtxCancelReturnsFalse covers the cancelled-select
// branch of the connector-local emitItem helper.
func TestEmitItem_CtxCancelReturnsFalse(t *testing.T) {
	items := make(chan model.FetchItem) // unbuffered, no receiver
	ctx, cancel := newTestContextDone()
	defer cancel()
	sid := "x"
	if emitItem(ctx, items, model.FetchItem{SourceID: &sid}) {
		t.Error("expected emitItem to return false on cancelled ctx")
	}
}
