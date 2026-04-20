package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/model"
)

// These tests cover the early-return branches of the relation-resolver
// helpers that don't reach the search client, so they don't need the
// integration test stack (Postgres + OpenSearch). The happy paths are
// exercised in integration_test.go's TestRelated_* suite.

func TestFirstReadableChunk(t *testing.T) {
	userID := uuid.New()
	owned := userID.String()
	stranger := uuid.New().String()

	claims := &auth.Claims{UserID: userID, Role: "user"}

	hits := []model.Chunk{
		{OwnerID: stranger, Shared: false, SourceType: "telegram"},
		{OwnerID: owned, Shared: false, SourceType: "telegram"},
		{OwnerID: owned, Shared: false, SourceType: "imap"},
	}

	t.Run("no filter returns first readable", func(t *testing.T) {
		ch := firstReadableChunk(hits, claims, "")
		if ch == nil || ch.SourceType != "telegram" || ch.OwnerID != owned {
			t.Errorf("expected readable telegram hit, got %+v", ch)
		}
	})

	t.Run("filter skips non-matching source_type", func(t *testing.T) {
		ch := firstReadableChunk(hits, claims, "imap")
		if ch == nil || ch.SourceType != "imap" {
			t.Errorf("expected imap hit, got %+v", ch)
		}
	})

	t.Run("returns nil when nothing readable", func(t *testing.T) {
		foreign := &auth.Claims{UserID: uuid.New(), Role: "user"}
		if ch := firstReadableChunk(hits, foreign, ""); ch != nil {
			t.Errorf("expected nil for foreign claims, got %+v", ch)
		}
	})
}

// The following tests exercise branches in the resolve* helpers that
// return before touching the search client, so a zero-value handler is
// enough. We use a t.Helper() wrapper to make the intent obvious.
func newEmptyHandler() *handler { return &handler{} }

func TestResolveByTargetID_EmptyID(t *testing.T) {
	h := newEmptyHandler()
	if doc := h.resolveByTargetID(context.Background(), "", &auth.Claims{}); doc != nil {
		t.Errorf("expected nil for empty targetID, got %+v", doc)
	}
}

func TestResolveByIMAPMessageID_NonIMAPSource(t *testing.T) {
	h := newEmptyHandler()
	rel := model.Relation{Type: model.RelationReplyTo, TargetSourceID: "<msg-id>"}
	if doc := h.resolveByIMAPMessageID(context.Background(), rel, "telegram", &auth.Claims{}); doc != nil {
		t.Errorf("expected nil for non-IMAP source_type, got %+v", doc)
	}
}

func TestResolveByIMAPMessageID_WrongRelationKind(t *testing.T) {
	h := newEmptyHandler()
	// attachment_of is IMAP-valid but resolveByIMAPMessageID only handles
	// reply_to / member_of_thread (Message-ID valued edges). Any other
	// relation kind on an IMAP doc should short-circuit.
	rel := model.Relation{Type: model.RelationAttachmentOf, TargetSourceID: "mail:123"}
	if doc := h.resolveByIMAPMessageID(context.Background(), rel, "imap", &auth.Claims{}); doc != nil {
		t.Errorf("expected nil for attachment_of kind, got %+v", doc)
	}
}

func TestResolveRelationTarget_NoIdentifiers(t *testing.T) {
	h := newEmptyHandler()
	// With both TargetID and TargetSourceID empty, resolveByTargetID
	// returns nil via the empty-id guard, then resolveRelationTarget
	// hits its own `TargetSourceID == ""` early return.
	rel := model.Relation{Type: model.RelationReplyTo}
	if doc := h.resolveRelationTarget(context.Background(), rel, "imap", &auth.Claims{}); doc != nil {
		t.Errorf("expected nil when both identifiers empty, got %+v", doc)
	}
}
