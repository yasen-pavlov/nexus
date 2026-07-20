package telegram

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
)

// TestFetchAllDialogs_MergesAcrossPages pins that pagination follows the
// *MessagesDialogsSlice "there may be more" shape to a second page and merges
// chats from both — accounts with >100 dialogs must not be truncated to page 1.
func TestFetchAllDialogs_MergesAcrossPages(t *testing.T) {
	const topMsg = 500
	api := &mockTelegramAPI{
		dialogPages: []tg.MessagesDialogsClass{
			&tg.MessagesDialogsSlice{
				Count: 2,
				Chats: []tg.ChatClass{&tg.Chat{ID: 1, Title: "Group1"}},
				Dialogs: []tg.DialogClass{
					&tg.Dialog{Peer: &tg.PeerChat{ChatID: 1}, TopMessage: topMsg},
				},
				Messages: []tg.MessageClass{&tg.Message{ID: topMsg, Date: 12345}},
			},
			&tg.MessagesDialogs{ // complete — ends pagination
				Chats: []tg.ChatClass{&tg.Chat{ID: 2, Title: "Group2"}},
			},
		},
	}

	chats, _, err := fetchAllDialogs(context.Background(), api)
	if err != nil {
		t.Fatal(err)
	}
	if api.dialogCalls != 2 {
		t.Errorf("expected 2 dialog page fetches, got %d", api.dialogCalls)
	}
	ids := map[string]bool{}
	for _, c := range chats {
		ids[chatIdentifier(c)] = true
	}
	if !ids["1"] || !ids["2"] {
		t.Errorf("expected chats from both pages, got %v", ids)
	}
}

// TestFetchAllDialogs_TerminatesOnNonAdvancingOffset guards the infinite-loop
// backstop: a server that keeps returning the same slice yields the same offset
// each time, so the loop must stop rather than spin forever.
func TestFetchAllDialogs_TerminatesOnNonAdvancingOffset(t *testing.T) {
	page := &tg.MessagesDialogsSlice{
		Count: 999,
		Chats: []tg.ChatClass{&tg.Chat{ID: 1}},
		Dialogs: []tg.DialogClass{
			&tg.Dialog{Peer: &tg.PeerChat{ChatID: 1}, TopMessage: 500},
		},
		Messages: []tg.MessageClass{&tg.Message{ID: 500, Date: 12345}},
	}
	api := &mockTelegramAPI{dialogPages: []tg.MessagesDialogsClass{page}}

	if _, _, err := fetchAllDialogs(context.Background(), api); err != nil {
		t.Fatal(err)
	}
	if api.dialogCalls > 3 {
		t.Errorf("non-advancing offset must terminate quickly, got %d calls", api.dialogCalls)
	}
}

func TestNextDialogOffset(t *testing.T) {
	const now = 12345
	slice := &tg.MessagesDialogsSlice{
		Chats: []tg.ChatClass{&tg.Chat{ID: 7}},
		Dialogs: []tg.DialogClass{
			&tg.Dialog{Peer: &tg.PeerChat{ChatID: 7}, TopMessage: 42},
		},
		Messages: []tg.MessageClass{&tg.Message{ID: 42, Date: now}},
	}
	date, id, peer, ok := nextDialogOffset(slice)
	if !ok || id != 42 || date != now || peer == nil {
		t.Fatalf("nextDialogOffset = (date=%d, id=%d, peer=%v, ok=%v)", date, id, peer, ok)
	}
	if _, _, _, ok := nextDialogOffset(&tg.MessagesDialogsSlice{}); ok {
		t.Error("empty Dialogs should return ok=false")
	}
}

func TestResolveOffsetPeer(t *testing.T) {
	users := []tg.UserClass{&tg.User{ID: 10, AccessHash: 111}}
	chats := []tg.ChatClass{&tg.Channel{ID: 20, AccessHash: 222}}

	if p, ok := resolveOffsetPeer(&tg.PeerUser{UserID: 10}, chats, users).(*tg.InputPeerUser); !ok || p.AccessHash != 111 {
		t.Errorf("PeerUser did not resolve AccessHash from users")
	}
	if p, ok := resolveOffsetPeer(&tg.PeerChannel{ChannelID: 20}, chats, users).(*tg.InputPeerChannel); !ok || p.AccessHash != 222 {
		t.Errorf("PeerChannel did not resolve AccessHash from chats")
	}
	if _, ok := resolveOffsetPeer(&tg.PeerChat{ChatID: 5}, chats, users).(*tg.InputPeerChat); !ok {
		t.Errorf("PeerChat should resolve without a hash")
	}
	if resolveOffsetPeer(&tg.PeerUser{UserID: 999}, chats, users) != nil {
		t.Errorf("unknown PeerUser should resolve to nil")
	}
}
