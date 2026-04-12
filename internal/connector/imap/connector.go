// Package imap implements a connector for IMAP email servers.
package imap

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
)

// mailboxClient abstracts IMAP operations for testability.
type mailboxClient interface {
	// SelectFolder selects a mailbox.
	SelectFolder(folder string) error
	// SearchUIDs returns UIDs matching the criteria.
	SearchUIDs(criteria *imap.SearchCriteria) ([]imap.UID, error)
	// FetchMessages fetches messages by UID set.
	FetchMessages(uids []imap.UID) ([]*imapclient.FetchMessageBuffer, error)
}

// realMailboxClient wraps an imapclient.Client to satisfy mailboxClient.
type realMailboxClient struct {
	client *imapclient.Client
}

func (r *realMailboxClient) SelectFolder(folder string) error {
	_, err := r.client.Select(folder, nil).Wait()
	return err
}

func (r *realMailboxClient) SearchUIDs(criteria *imap.SearchCriteria) ([]imap.UID, error) {
	data, err := r.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}
	return data.AllUIDs(), nil
}

func (r *realMailboxClient) FetchMessages(uids []imap.UID) ([]*imapclient.FetchMessageBuffer, error) {
	uidSet := imap.UIDSetNum(uids...)
	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{
			{}, // entire message body for MIME parsing
		},
	}
	return r.client.Fetch(uidSet, fetchOptions).Collect()
}

// dialFunc allows overriding the IMAP connection for testing.
type dialFunc func(address string, options *imapclient.Options) (*imapclient.Client, error)

func init() {
	connector.Register("imap", func() connector.Connector {
		return &Connector{
			port:    993,
			folders: []string{"INBOX"},
			dial:    imapclient.DialTLS,
		}
	})
}

// Connector fetches emails from an IMAP server.
type Connector struct {
	name        string
	server      string
	port        int
	username    string
	password    string
	folders     []string
	syncSince   time.Time
	extractor   *extractor.Registry
	dial        dialFunc
	binaryStore connector.BinaryStoreAPI
	cacheConfig connector.CacheConfig
}

func (c *Connector) Type() string { return "imap" }
func (c *Connector) Name() string { return c.name }

// SetExtractor sets the content extractor for email attachments.
func (c *Connector) SetExtractor(ext *extractor.Registry) {
	c.extractor = ext
}

// SetBinaryStore wires the binary content cache plus the resolved
// per-connector cache policy. Implements connector.CacheAware.
// Called once at wire time by ConnectorManager.instantiateConnector.
func (c *Connector) SetBinaryStore(store connector.BinaryStoreAPI, cfg connector.CacheConfig) {
	c.binaryStore = store
	c.cacheConfig = cfg
}

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "imap"
	}
	c.name = name

	server := cfg.StringVal("server")
	if server == "" {
		return fmt.Errorf("imap: server is required")
	}
	c.server = server

	if portStr := cfg.StringVal("port"); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("imap: invalid port: %w", err)
		}
		c.port = p
	}

	username := cfg.StringVal("username")
	if username == "" {
		return fmt.Errorf("imap: username is required")
	}
	c.username = username

	password := cfg.StringVal("password")
	if password == "" {
		return fmt.Errorf("imap: password is required")
	}
	c.password = password

	if folders := cfg.StringVal("folders"); folders != "" {
		c.folders = nil
		for _, f := range strings.Split(folders, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				c.folders = append(c.folders, f)
			}
		}
	}

	c.syncSince = connector.ComputeSyncSince(cfg)

	return nil
}

func (c *Connector) Validate() error {
	if c.server == "" || c.username == "" || c.password == "" {
		return fmt.Errorf("imap: server, username, and password are required")
	}

	addr := fmt.Sprintf("%s:%d", c.server, c.port)
	client, err := c.dial(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: c.server},
	})
	if err != nil {
		return fmt.Errorf("imap: cannot connect to %s: %w", addr, err)
	}
	defer client.Close() //nolint:errcheck // best-effort close

	if err := client.Login(c.username, c.password).Wait(); err != nil {
		return fmt.Errorf("imap: authentication failed: %w", err)
	}

	_ = client.Logout().Wait()
	return nil
}

func (c *Connector) Fetch(ctx context.Context, cursor *model.SyncCursor) (*model.FetchResult, error) {
	addr := fmt.Sprintf("%s:%d", c.server, c.port)
	client, err := c.dial(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: c.server},
	})
	if err != nil {
		return nil, fmt.Errorf("imap: connect: %w", err)
	}
	defer client.Close() //nolint:errcheck // best-effort close

	if err := client.Login(c.username, c.password).Wait(); err != nil {
		return nil, fmt.Errorf("imap: login: %w", err)
	}

	mbc := &realMailboxClient{client: client}
	docs, newUIDs, err := c.fetchWithClient(ctx, mbc, cursor)
	if err != nil {
		return nil, err
	}

	_ = client.Logout().Wait()

	now := time.Now()
	cursorData := map[string]any{}
	for folder, uid := range newUIDs {
		cursorData["uid:"+folder] = float64(uid)
	}

	return &model.FetchResult{
		Documents: docs,
		Cursor: &model.SyncCursor{
			CursorData:  cursorData,
			LastSync:    now,
			LastStatus:  "success",
			ItemsSynced: len(docs),
		},
	}, nil
}

func (c *Connector) fetchWithClient(ctx context.Context, mbc mailboxClient, cursor *model.SyncCursor) ([]model.Document, map[string]imap.UID, error) {
	var allDocs []model.Document
	newUIDs := make(map[string]imap.UID)

	for _, folder := range c.folders {
		if ctx.Err() != nil {
			return allDocs, newUIDs, ctx.Err()
		}

		docs, lastUID, err := c.fetchFolder(ctx, mbc, folder, cursor)
		if err != nil {
			return allDocs, newUIDs, fmt.Errorf("imap: folder %q: %w", folder, err)
		}

		allDocs = append(allDocs, docs...)
		if lastUID > 0 {
			newUIDs[folder] = lastUID
		}
	}

	return allDocs, newUIDs, nil
}

func (c *Connector) fetchFolder(ctx context.Context, mbc mailboxClient, folder string, cursor *model.SyncCursor) ([]model.Document, imap.UID, error) {
	if err := mbc.SelectFolder(folder); err != nil {
		return nil, 0, fmt.Errorf("select: %w", err)
	}

	criteria := &imap.SearchCriteria{}

	var lastUID imap.UID
	if cursor != nil {
		if uidVal, ok := cursor.CursorData["uid:"+folder].(float64); ok {
			lastUID = imap.UID(uidVal)
		}
	}

	if lastUID > 0 {
		uidSet := imap.UIDSet{}
		uidSet.AddRange(lastUID+1, 0) // 0 means * (all)
		criteria.UID = []imap.UIDSet{uidSet}
	} else if !c.syncSince.IsZero() {
		criteria.Since = c.syncSince
	}

	uids, err := mbc.SearchUIDs(criteria)
	if err != nil {
		return nil, 0, fmt.Errorf("search: %w", err)
	}

	if len(uids) == 0 {
		return nil, lastUID, nil
	}

	msgs, err := mbc.FetchMessages(uids)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch: %w", err)
	}

	var docs []model.Document
	var maxUID imap.UID

	for _, msg := range msgs {
		if ctx.Err() != nil {
			return docs, maxUID, ctx.Err()
		}

		if msg.UID > maxUID {
			maxUID = msg.UID
		}

		msgDocs := c.messageToDocuments(msg, folder)
		docs = append(docs, msgDocs...)
	}

	if maxUID > lastUID {
		lastUID = maxUID
	}

	return docs, lastUID, nil
}

func (c *Connector) messageToDocuments(msg *imapclient.FetchMessageBuffer, folder string) []model.Document {
	if msg.Envelope == nil {
		return nil
	}

	env := msg.Envelope

	// Get the raw body for MIME parsing
	var bodyData []byte
	for _, section := range msg.BodySection {
		bodyData = section.Bytes
		break
	}

	textContent, attachments := parseEmailBody(bodyData)
	// If MIME parsing fails to extract any plain text we used to fall back to
	// the raw RFC822 source. That's almost guaranteed to contain large base64
	// blobs and other non-text noise — useless for search and prone to blowing
	// past embedding-API token limits. Skip these messages instead.

	// Build metadata
	metadata := map[string]any{
		"folder":     folder,
		"message_id": env.MessageID,
		"date":       env.Date.Format(time.RFC3339),
	}

	if len(env.From) > 0 {
		metadata["from"] = formatAddresses(env.From)
	}
	if len(env.To) > 0 {
		metadata["to"] = formatAddresses(env.To)
	}
	if len(env.Cc) > 0 {
		metadata["cc"] = formatAddresses(env.Cc)
	}
	if len(attachments) > 0 {
		metadata["has_attachments"] = true
		names := make([]string, 0, len(attachments))
		for _, a := range attachments {
			if a.Filename != "" {
				names = append(names, a.Filename)
			}
		}
		if len(names) > 0 {
			metadata["attachment_filenames"] = names
		}
	}

	// Build URL using mid: URI scheme
	var msgURL string
	if env.MessageID != "" {
		msgURL = "mid:" + env.MessageID
	}

	createdAt := env.Date
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	var docs []model.Document

	// Main email document
	emailSourceID := fmt.Sprintf("%s:%d", folder, msg.UID)
	docs = append(docs, model.Document{
		ID:         model.DocumentID("imap", c.name, emailSourceID),
		SourceType: "imap",
		SourceName: c.name,
		SourceID:   emailSourceID,
		Title:      env.Subject,
		Content:    textContent,
		Metadata:   metadata,
		URL:        msgURL,
		Visibility: "private",
		CreatedAt:  createdAt,
	})

	// Attachment documents — always emitted, even when extraction fails or is
	// unsupported. The doc carries empty content in that case but full metadata
	// (filename, mime type, size) so it remains discoverable and previewable
	// once an IMAP BinaryFetcher is implemented.
	for i, att := range attachments {
		var extracted string
		if c.extractor != nil && c.extractor.CanExtract(att.ContentType) {
			if out, err := c.extractor.Extract(context.Background(), att.ContentType, att.Data); err == nil {
				extracted = out
			}
		}

		attMetadata := map[string]any{
			"parent_message_id": env.MessageID,
			"parent_subject":    env.Subject,
			"attachment_index":  i,
			"content_type":      att.ContentType,
		}
		if att.Filename != "" {
			attMetadata["filename"] = att.Filename
		}

		attSourceID := fmt.Sprintf("%s:%d:attachment:%d", folder, msg.UID, i)
		docs = append(docs, model.Document{
			ID:         model.DocumentID("imap", c.name, attSourceID),
			SourceType: "imap",
			SourceName: c.name,
			SourceID:   attSourceID,
			Title:      att.Filename,
			Content:    extracted,
			MimeType:   att.ContentType,
			Size:       int64(len(att.Data)),
			Metadata:   attMetadata,
			URL:        msgURL,
			Visibility: "private",
			CreatedAt:  createdAt,
		})

		// Eager cache population: the attachment bytes are already in
		// memory from the MIME parse — dropping them here means the
		// first preview click would have to re-fetch the entire email
		// from IMAP. When the admin has opted into eager mode on this
		// connector we proactively cache. Best-effort — a cache write
		// failure shouldn't abort the sync.
		if c.binaryStore != nil && c.cacheConfig.Mode == "eager" {
			_ = c.binaryStore.Put(
				context.Background(),
				"imap", c.name, attSourceID,
				bytes.NewReader(att.Data),
				int64(len(att.Data)),
			)
		}
	}

	return docs
}

func formatAddresses(addrs []imap.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.Name, a.Addr()))
		} else {
			parts = append(parts, a.Addr())
		}
	}
	return strings.Join(parts, ", ")
}
