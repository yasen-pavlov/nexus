package api

import (
	"testing"

	"github.com/muty/nexus/internal/model"
)

func TestFindInboundRelation(t *testing.T) {
	docID := "doc-uuid"
	tests := []struct {
		name       string
		rels       []model.Relation
		targetID   string
		targetSIDs []string
		wantType   string // "" means empty relation (no match)
	}{
		{
			name: "match by target_id",
			rels: []model.Relation{
				{Type: "reply_to", TargetID: "other-uuid"},
				{Type: "attachment_of", TargetID: docID},
			},
			targetID: docID,
			wantType: "attachment_of",
		},
		{
			name: "match by target_source_id",
			rels: []model.Relation{
				{Type: "reply_to", TargetSourceID: "INBOX:1"},
			},
			targetID:   "no-match",
			targetSIDs: []string{"INBOX:1"},
			wantType:   "reply_to",
		},
		{
			name:       "no match returns zero relation",
			rels:       []model.Relation{{Type: "reply_to", TargetSourceID: "ghost"}},
			targetID:   docID,
			targetSIDs: []string{"other"},
			wantType:   "",
		},
		{
			name:     "empty relations",
			rels:     nil,
			targetID: docID,
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findInboundRelation(tt.rels, tt.targetID, tt.targetSIDs)
			if got.Type != tt.wantType {
				t.Errorf("got type %q, want %q", got.Type, tt.wantType)
			}
		})
	}
}
