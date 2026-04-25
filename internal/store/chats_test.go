//go:build integration

package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

func seedUser(t *testing.T, st *Store, username string) uuid.UUID {
	t.Helper()
	u, err := st.CreateUser(context.Background(), username, "hash", "user")
	if err != nil {
		t.Fatalf("seed user %q: %v", username, err)
	}
	return u.ID
}

func newChat(userID uuid.UUID) *model.Chat {
	return &model.Chat{
		UserID:       userID,
		Title:        "untitled",
		DefaultModel: "anthropic:claude-sonnet-4-6",
	}
}

func TestCreateAndGetChat(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-create")

	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}
	if chat.ID == uuid.Nil {
		t.Fatal("expected ID populated")
	}
	if chat.CreatedAt.IsZero() || chat.UpdatedAt.IsZero() {
		t.Fatal("expected timestamps populated")
	}

	got, err := st.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "untitled" {
		t.Errorf("title=%q", got.Title)
	}
	if got.DefaultModel != "anthropic:claude-sonnet-4-6" {
		t.Errorf("default_model=%q", got.DefaultModel)
	}
	if got.UserID != userID {
		t.Errorf("user_id mismatch")
	}
}

func TestGetChat_NotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetChat(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestListChats_OrderingAndPagination(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-list")
	other := seedUser(t, st, "chat-list-other")

	// Three chats, each visible after the previous (so ordering by
	// updated_at desc gives c3, c2, c1).
	for _, title := range []string{"first", "second", "third"} {
		c := newChat(userID)
		c.Title = title
		if err := st.CreateChat(ctx, c); err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
	}
	// One chat for a different user — must not appear in our listing.
	otherChat := newChat(other)
	otherChat.Title = "stranger"
	if err := st.CreateChat(ctx, otherChat); err != nil {
		t.Fatalf("create stranger: %v", err)
	}

	chats, total, err := st.ListChats(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("total=%d", total)
	}
	if len(chats) != 3 {
		t.Fatalf("got %d chats", len(chats))
	}
	if chats[0].Title != "third" || chats[2].Title != "first" {
		t.Errorf("order wrong: %v", []string{chats[0].Title, chats[1].Title, chats[2].Title})
	}
	for _, c := range chats {
		if c.FirstMessagePreview != "" {
			t.Errorf("expected empty preview for chat %q with no messages, got %q", c.Title, c.FirstMessagePreview)
		}
	}

	// Pagination
	page2, total2, err := st.ListChats(ctx, userID, 2, 2)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total2 != 3 {
		t.Errorf("total page 2 = %d", total2)
	}
	if len(page2) != 1 || page2[0].Title != "first" {
		t.Errorf("page 2 = %+v", page2)
	}
}

func TestUpdateChat_PartialFields(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-update")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	newTitle := "renamed"
	if err := st.UpdateChat(ctx, chat.ID, ChatUpdate{Title: &newTitle}); err != nil {
		t.Fatalf("update title: %v", err)
	}
	got, _ := st.GetChat(ctx, chat.ID)
	if got.Title != "renamed" {
		t.Errorf("title=%q", got.Title)
	}
	if got.DefaultModel != chat.DefaultModel {
		t.Errorf("default_model wiped: %q", got.DefaultModel)
	}

	newModel := "openai:gpt-5-mini"
	if err := st.UpdateChat(ctx, chat.ID, ChatUpdate{DefaultModel: &newModel}); err != nil {
		t.Fatalf("update model: %v", err)
	}
	got, _ = st.GetChat(ctx, chat.ID)
	if got.DefaultModel != "openai:gpt-5-mini" {
		t.Errorf("model not updated: %q", got.DefaultModel)
	}
	if got.Title != "renamed" {
		t.Errorf("title overwritten: %q", got.Title)
	}

	// Empty update still bumps updated_at
	if err := st.UpdateChat(ctx, chat.ID, ChatUpdate{}); err != nil {
		t.Fatalf("update empty: %v", err)
	}
}

func TestUpdateChat_NotFound(t *testing.T) {
	st := newTestStore(t)
	title := "x"
	err := st.UpdateChat(context.Background(), uuid.New(), ChatUpdate{Title: &title})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
	err = st.UpdateChat(context.Background(), uuid.New(), ChatUpdate{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("empty err=%v want ErrNotFound", err)
	}
}

func TestDeleteChat_CascadesMessages(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-delete")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.AppendMessage(ctx, &model.ChatMessage{
		ChatID:  chat.ID,
		Role:    model.ChatRoleUser,
		Content: "hello",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := st.DeleteChat(ctx, chat.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetChat(ctx, chat.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("chat still present: %v", err)
	}
	msgs, err := st.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatalf("list msgs: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("messages not cascaded: %d remain", len(msgs))
	}
}

func TestDeleteChat_NotFound(t *testing.T) {
	st := newTestStore(t)
	err := st.DeleteChat(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}

func TestAppendMessage_AssignsMonotonicSeq(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-seq")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	for i, content := range []string{"first", "second", "third"} {
		msg := &model.ChatMessage{
			ChatID:  chat.ID,
			Role:    model.ChatRoleUser,
			Content: content,
		}
		if err := st.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if msg.Seq != i+1 {
			t.Errorf("msg %d seq=%d want %d", i, msg.Seq, i+1)
		}
		if msg.ID == uuid.Nil {
			t.Errorf("msg %d ID not set", i)
		}
	}
	msgs, err := st.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d msgs", len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != i+1 {
			t.Errorf("msg[%d].Seq=%d want %d", i, m.Seq, i+1)
		}
	}
}

func TestAppendMessage_ConcurrentSerialisation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-concurrent")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	const N = 20
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := &model.ChatMessage{
				ChatID:  chat.ID,
				Role:    model.ChatRoleUser,
				Content: "concurrent",
			}
			if err := st.AppendMessage(ctx, msg); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("append failed: %v", err)
	}

	msgs, err := st.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != N {
		t.Fatalf("got %d msgs want %d", len(msgs), N)
	}
	seen := make(map[int]bool)
	for _, m := range msgs {
		if seen[m.Seq] {
			t.Errorf("duplicate seq %d", m.Seq)
		}
		seen[m.Seq] = true
		if m.Seq < 1 || m.Seq > N {
			t.Errorf("seq out of range: %d", m.Seq)
		}
	}
}

func TestAppendMessage_ChatNotFound(t *testing.T) {
	st := newTestStore(t)
	err := st.AppendMessage(context.Background(), &model.ChatMessage{
		ChatID:  uuid.New(),
		Role:    model.ChatRoleUser,
		Content: "orphan",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}

func TestAppendMessage_RoundtripsAssistantMetadata(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-meta")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	cite := model.ChatCitation{DocID: "doc-1", CitedText: "snippet", SpanStart: 5, SpanEnd: 12}
	usage := &model.ChatUsage{Input: 100, Output: 50, CacheRead: 10, CacheWrite: 200}

	asst := &model.ChatMessage{
		ChatID:     chat.ID,
		Role:       model.ChatRoleAssistant,
		Content:    "answer",
		Model:      "anthropic:claude-sonnet-4-6",
		Citations:  []model.ChatCitation{cite},
		Usage:      usage,
		StopReason: "end_turn",
	}
	if err := st.AppendMessage(ctx, asst); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	msgs, err := st.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d", len(msgs))
	}
	got := msgs[0]
	if got.Model != "anthropic:claude-sonnet-4-6" {
		t.Errorf("model=%q", got.Model)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q", got.StopReason)
	}
	if len(got.Citations) != 1 || got.Citations[0] != cite {
		t.Errorf("citations=%+v", got.Citations)
	}
	if got.Usage == nil || *got.Usage != *usage {
		t.Errorf("usage=%+v", got.Usage)
	}
}

func TestAppendMessage_NilMetadataStaysNil(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-nilmeta")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.AppendMessage(ctx, &model.ChatMessage{
		ChatID:  chat.ID,
		Role:    model.ChatRoleUser,
		Content: "plain user",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	msgs, _ := st.ListMessages(ctx, chat.ID)
	if msgs[0].Citations != nil {
		t.Errorf("expected nil citations, got %+v", msgs[0].Citations)
	}
	if msgs[0].Usage != nil {
		t.Errorf("expected nil usage, got %+v", msgs[0].Usage)
	}
	if msgs[0].StopReason != "" {
		t.Errorf("stop_reason=%q", msgs[0].StopReason)
	}
	if msgs[0].Model != "" {
		t.Errorf("model=%q", msgs[0].Model)
	}
}

func TestChatStore_ErrorsWhenPoolClosed(t *testing.T) {
	st := newClosedStore(t)
	ctx := context.Background()
	id := uuid.New()
	user := uuid.New()

	if err := st.CreateChat(ctx, &model.Chat{UserID: user}); err == nil {
		t.Error("CreateChat with closed pool: nil err")
	}
	if _, err := st.GetChat(ctx, id); err == nil {
		t.Error("GetChat with closed pool: nil err")
	}
	if _, _, err := st.ListChats(ctx, user, 10, 0); err == nil {
		t.Error("ListChats with closed pool: nil err")
	}
	t2 := "x"
	if err := st.UpdateChat(ctx, id, ChatUpdate{Title: &t2}); err == nil {
		t.Error("UpdateChat with closed pool: nil err")
	}
	if err := st.UpdateChat(ctx, id, ChatUpdate{}); err == nil {
		t.Error("UpdateChat empty with closed pool: nil err")
	}
	if err := st.DeleteChat(ctx, id); err == nil {
		t.Error("DeleteChat with closed pool: nil err")
	}
	if _, err := st.ListMessages(ctx, id); err == nil {
		t.Error("ListMessages with closed pool: nil err")
	}
	if err := st.AppendMessage(ctx, &model.ChatMessage{ChatID: id, Role: model.ChatRoleUser, Content: "x"}); err == nil {
		t.Error("AppendMessage with closed pool: nil err")
	}
}

func TestListChats_AppliesDefaultLimitAndOffset(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-defaults")
	for i := 0; i < 3; i++ {
		c := newChat(userID)
		if err := st.CreateChat(ctx, c); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	chats, total, err := st.ListChats(ctx, userID, 0, -5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("total=%d", total)
	}
	if len(chats) != 3 {
		t.Errorf("chats=%d", len(chats))
	}
}

func TestListChats_FirstMessagePreview(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-preview")

	// Two chats: one with a user message, one without.
	withMsg := newChat(userID)
	if err := st.CreateChat(ctx, withMsg); err != nil {
		t.Fatalf("create with-msg: %v", err)
	}
	bare := newChat(userID)
	if err := st.CreateChat(ctx, bare); err != nil {
		t.Fatalf("create bare: %v", err)
	}

	if err := st.AppendMessage(ctx, &model.ChatMessage{
		ChatID:  withMsg.ID,
		Role:    model.ChatRoleUser,
		Content: "What did Anthropic invoice me last month?",
	}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	// Assistant turn arrives later — must NOT shadow the user preview.
	if err := st.AppendMessage(ctx, &model.ChatMessage{
		ChatID:  withMsg.ID,
		Role:    model.ChatRoleAssistant,
		Content: "I found three invoices...",
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	chats, _, err := st.ListChats(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("want 2, got %d", len(chats))
	}
	// Order is updated_at desc — the chat that just got messages bumped
	// is now first.
	if chats[0].ID != withMsg.ID {
		t.Fatalf("expected with-msg first; got %v", chats[0].ID)
	}
	if chats[0].FirstMessagePreview != "What did Anthropic invoice me last month?" {
		t.Errorf("preview=%q", chats[0].FirstMessagePreview)
	}
	if chats[1].FirstMessagePreview != "" {
		t.Errorf("bare chat should have empty preview, got %q", chats[1].FirstMessagePreview)
	}
}

func TestListChats_PreviewTruncatedToCap(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-preview-cap")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	long := make([]byte, chatPreviewMaxChars+50)
	for i := range long {
		long[i] = 'x'
	}
	if err := st.AppendMessage(ctx, &model.ChatMessage{
		ChatID:  chat.ID,
		Role:    model.ChatRoleUser,
		Content: string(long),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	chats, _, err := st.ListChats(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := len(chats[0].FirstMessagePreview); got != chatPreviewMaxChars {
		t.Errorf("preview length=%d want=%d", got, chatPreviewMaxChars)
	}
}

func TestListMessages_EmptyChatReturnsEmptySlice(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	userID := seedUser(t, st, "chat-empty")
	chat := newChat(userID)
	if err := st.CreateChat(ctx, chat); err != nil {
		t.Fatalf("create: %v", err)
	}

	msgs, err := st.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if msgs == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(msgs) != 0 {
		t.Errorf("got %d msgs", len(msgs))
	}
}
