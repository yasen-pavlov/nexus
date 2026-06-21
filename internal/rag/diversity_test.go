package rag

import (
	"testing"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

// tgHit builds a Telegram conversation-window hit with the given chat id
// (the part before the colon in SourceID) and message range.
func tgHit(chatID, msgRange string) model.DocumentHit {
	return model.DocumentHit{Document: model.Document{
		ID:         uuid.New(),
		SourceType: "telegram",
		SourceID:   chatID + ":" + msgRange,
	}}
}

func srcHit(sourceType, sourceID string) model.DocumentHit {
	return model.DocumentHit{Document: model.Document{
		ID:         uuid.New(),
		SourceType: sourceType,
		SourceID:   sourceID,
	}}
}

func keysOf(hits []model.DocumentHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = conversationKey(h.Document)
	}
	return out
}

func TestCapPerConversation_CapsTelegramHubChannel(t *testing.T) {
	// One bot-notification channel (chat 3938898465) saturates retrieval
	// with 5 windows; a real paperless doc and a second chat sit lower.
	hits := []model.DocumentHit{
		tgHit("3938898465", "1-10"),
		tgHit("3938898465", "11-20"),
		tgHit("3938898465", "21-30"),
		tgHit("3938898465", "31-40"),
		srcHit("paperless", "26"),
		tgHit("3938898465", "41-50"),
		tgHit("607901906", "5-9"),
	}

	got := capPerConversation(hits, 3)

	// The hub chat is capped to 3 windows; everything else survives in order.
	if len(got) != 5 {
		t.Fatalf("want 5 hits after cap, got %d: %v", len(got), keysOf(got))
	}
	var hubCount int
	var sawPaperless, sawSecondChat bool
	for _, h := range got {
		switch conversationKey(h.Document) {
		case "telegram|chat|3938898465":
			hubCount++
		case "paperless|doc|26":
			sawPaperless = true
		case "telegram|chat|607901906":
			sawSecondChat = true
		}
	}
	if hubCount != 3 {
		t.Errorf("hub channel should be capped to 3, got %d", hubCount)
	}
	if !sawPaperless {
		t.Error("the paperless doc must survive the cap")
	}
	if !sawSecondChat {
		t.Error("a different telegram chat must not be capped against the hub")
	}
}

func TestCapPerConversation_PreservesOrder(t *testing.T) {
	hits := []model.DocumentHit{
		tgHit("A", "1-1"),
		srcHit("paperless", "1"),
		tgHit("A", "2-2"),
		tgHit("A", "3-3"), // dropped (cap 2 for chat A)
		srcHit("paperless", "2"),
	}
	got := capPerConversation(hits, 2)
	want := []string{
		"telegram|chat|A",
		"paperless|doc|1",
		"telegram|chat|A",
		"paperless|doc|2",
	}
	if gotKeys := keysOf(got); !equalStrings(gotKeys, want) {
		t.Errorf("order/cap wrong:\n got %v\nwant %v", gotKeys, want)
	}
}

func TestCapPerConversation_DisabledOrSmall(t *testing.T) {
	hits := []model.DocumentHit{tgHit("A", "1-1"), tgHit("A", "2-2"), tgHit("A", "3-3")}
	if got := capPerConversation(hits, 0); len(got) != 3 {
		t.Errorf("max<=0 disables: want 3, got %d", len(got))
	}
	if got := capPerConversation(hits, -1); len(got) != 3 {
		t.Errorf("negative max disables: want 3, got %d", len(got))
	}
	if got := capPerConversation(hits, 5); len(got) != 3 {
		t.Errorf("len<=max is a no-op: want 3, got %d", len(got))
	}
}

func TestConversationKey(t *testing.T) {
	cases := []struct {
		name string
		doc  model.Document
		want string
	}{
		{
			name: "telegram keys on chat id before the colon",
			doc:  model.Document{SourceType: "telegram", SourceID: "3938898465:1073-1080"},
			want: "telegram|chat|3938898465",
		},
		{
			name: "telegram per-message child still keys on chat id",
			doc:  model.Document{SourceType: "telegram", SourceID: "3938898465:1073-1080:1075"},
			want: "telegram|chat|3938898465",
		},
		{
			name: "paperless keys on source document id",
			doc:  model.Document{SourceType: "paperless", SourceID: "26"},
			want: "paperless|doc|26",
		},
		{
			name: "explicit conversation id wins",
			doc:  model.Document{SourceType: "telegram", SourceID: "x:1-2", ConversationID: "conv-42"},
			want: "telegram|conv|conv-42",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conversationKey(tc.doc); got != tc.want {
				t.Errorf("conversationKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
