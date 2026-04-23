package telegram

import (
	"context"
	"strconv"

	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/model"
)

// processDialogs is a test-only shim that drains the streaming
// streamWithAPI path (via injected dialog roster) into a slice of
// documents. Exists so the existing test corpus — which was written
// against the batch-returning pre-streaming API — keeps working
// without a per-test rewrite. Production code calls streamWithAPI
// directly via Fetch.
func (c *Connector) processDialogs(ctx context.Context, api telegramAPI, dl mediaDownloader, chats []tg.ChatClass, users []tg.UserClass, userMap map[int64]*tg.User, selfID int64, sinceDate int) ([]model.Document, error) {
	items := make(chan model.FetchItem, 1024)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		if userMap == nil {
			userMap = buildUserMap(users)
		}
		if err := c.streamDialogsToItems(ctx, api, dl, chats, users, userMap, selfID, sinceDate, items); err != nil {
			errs <- err
		}
	}()
	return drainDocs(items, errs)
}

// fetchChatMessages is a test-only shim mirroring processDialogs but
// scoped to a single chat — wraps streamChat into a slice return.
func (c *Connector) fetchChatMessages(ctx context.Context, api telegramAPI, dl mediaDownloader, inputPeer tg.InputPeerClass, chatName, chatID string, userMap map[int64]*tg.User, selfID, dmPeerID int64, sinceDate int) ([]model.Document, error) {
	items := make(chan model.FetchItem, 1024)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		var est int64
		if err := c.streamChat(ctx, api, dl, inputPeer, chatName, chatID, userMap, selfID, dmPeerID, sinceDate, &est, items); err != nil {
			errs <- err
		}
	}()
	return drainDocs(items, errs)
}

// fetchWithAPI is a test-only shim matching the pre-streaming API
// signature. Drains streamWithAPI into a slice.
func (c *Connector) fetchWithAPI(ctx context.Context, api telegramAPI, dl mediaDownloader, cursor *model.SyncCursor, selfID int64) ([]model.Document, error) {
	items := make(chan model.FetchItem, 1024)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		if err := c.streamWithAPI(ctx, api, dl, cursor, selfID, items); err != nil {
			errs <- err
		}
	}()
	return drainDocs(items, errs)
}

// streamDialogsToItems is extracted from streamWithAPI so the
// processDialogs shim can drive it with a pre-built roster (tests
// often skip the MessagesGetDialogs call and hand the connector
// chats+users directly).
func (c *Connector) streamDialogsToItems(ctx context.Context, api telegramAPI, dl mediaDownloader, chats []tg.ChatClass, users []tg.UserClass, userMap map[int64]*tg.User, selfID int64, sinceDate int, items chan<- model.FetchItem) error {
	var estimate int64
	for _, chat := range chats {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		chatName := chatTitle(chat)
		chatID := chatIdentifier(chat)
		if !c.matchesChatFilter(chatName, chatID) {
			continue
		}
		inputPeer := chatToInputPeer(chat)
		if inputPeer == nil {
			continue
		}
		if err := c.streamChat(ctx, api, dl, inputPeer, chatName, chatID, userMap, selfID, 0, sinceDate, &estimate, items); err != nil {
			continue
		}
	}
	for _, user := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		u, ok := user.(*tg.User)
		if !ok || u.Bot || u.Self {
			continue
		}
		chatName := userDisplayName(u)
		chatID := strconv.FormatInt(u.ID, 10)
		if !c.matchesChatFilter(chatName, chatID) {
			continue
		}
		inputPeer := &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
		if err := c.streamChat(ctx, api, dl, inputPeer, chatName, chatID, userMap, selfID, u.ID, sinceDate, &estimate, items); err != nil {
			continue
		}
	}
	return nil
}

// drainDocs reads items + errs to completion and returns the
// Document payloads in the order emitted. Any terminal error is
// returned alongside the docs accumulated so far.
func drainDocs(items <-chan model.FetchItem, errs <-chan error) ([]model.Document, error) {
	var docs []model.Document
	var lastErr error
	itemsDone := false
	errsDone := false
	for !itemsDone || !errsDone {
		select {
		case it, ok := <-items:
			if !ok {
				itemsDone = true
				continue
			}
			if it.Doc != nil {
				docs = append(docs, *it.Doc)
			}
		case e, ok := <-errs:
			if !ok {
				errsDone = true
				continue
			}
			if e != nil {
				lastErr = e
			}
		}
	}
	return docs, lastErr
}
