// Package paperless implements a connector for Paperless-ngx document management system.
package paperless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
)

func init() {
	connector.Register("paperless", func() connector.Connector {
		return &Connector{client: &http.Client{Timeout: 30 * time.Second}}
	})
}

// Connector fetches documents from a Paperless-ngx instance.
type Connector struct {
	name      string
	baseURL   string
	token     string
	syncSince time.Time
	client    *http.Client
}

func (c *Connector) Type() string { return "paperless" }
func (c *Connector) Name() string { return c.name }

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "paperless"
	}
	c.name = name

	baseURL, _ := cfg["url"].(string)
	if baseURL == "" {
		return fmt.Errorf("paperless: url is required")
	}
	c.baseURL = strings.TrimRight(baseURL, "/")

	token, _ := cfg["token"].(string)
	if token == "" {
		return fmt.Errorf("paperless: token is required")
	}
	c.token = token
	c.syncSince = connector.ComputeSyncSince(cfg)

	return nil
}

func (c *Connector) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/documents/?page=1&page_size=1", nil)
	if err != nil {
		return fmt.Errorf("paperless: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("paperless: cannot connect to %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("paperless: authentication failed (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("paperless: unexpected status %d from %s", resp.StatusCode, c.baseURL)
	}

	return nil
}

func (c *Connector) Fetch(ctx context.Context, cursor *model.SyncCursor) (*model.FetchResult, error) {
	// Resolve lookup tables for human-readable metadata
	tags, err := c.fetchLookup(ctx, "/api/tags/")
	if err != nil {
		return nil, fmt.Errorf("paperless: fetch tags: %w", err)
	}
	correspondents, err := c.fetchLookup(ctx, "/api/correspondents/")
	if err != nil {
		return nil, fmt.Errorf("paperless: fetch correspondents: %w", err)
	}
	docTypes, err := c.fetchLookup(ctx, "/api/document_types/")
	if err != nil {
		return nil, fmt.Errorf("paperless: fetch document types: %w", err)
	}

	// Build initial URL
	params := url.Values{}
	params.Set("ordering", "modified")
	params.Set("page_size", "100")

	if cursor != nil {
		if ts, ok := cursor.CursorData["last_sync_time"].(string); ok {
			params.Set("modified__gt", ts)
		}
	} else if !c.syncSince.IsZero() {
		params.Set("modified__gt", c.syncSince.Format(time.RFC3339))
	}

	fetchURL := c.baseURL + "/api/documents/?" + params.Encode()

	var docs []model.Document
	for fetchURL != "" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		page, nextURL, err := c.fetchPage(ctx, fetchURL)
		if err != nil {
			return nil, fmt.Errorf("paperless: fetch page: %w", err)
		}

		for _, pdoc := range page {
			doc := c.toDocument(pdoc, tags, correspondents, docTypes)
			docs = append(docs, doc)
		}

		fetchURL = nextURL
	}

	now := time.Now()
	return &model.FetchResult{
		Documents: docs,
		Cursor: &model.SyncCursor{
			ConnectorID: c.Name(),
			CursorData: map[string]any{
				"last_sync_time": now.Format(time.RFC3339Nano),
			},
			LastSync:    now,
			LastStatus:  "success",
			ItemsSynced: len(docs),
		},
	}, nil
}

// paperlessDoc represents a document from the Paperless API.
type paperlessDoc struct {
	ID               int       `json:"id"`
	Title            string    `json:"title"`
	Content          string    `json:"content"`
	Correspondent    *int      `json:"correspondent"`
	DocumentType     *int      `json:"document_type"`
	Tags             []int     `json:"tags"`
	OriginalFileName string    `json:"original_file_name"`
	Created          string    `json:"created"` // document date (YYYY-MM-DD), when originally issued
	Added            time.Time `json:"added"`   // import date, when scanned into Paperless
	Modified         time.Time `json:"modified"`
}

type paginatedResponse struct {
	Count    int            `json:"count"`
	Next     *string        `json:"next"`
	Previous *string        `json:"previous"`
	Results  []paperlessDoc `json:"results"`
}

func (c *Connector) fetchPage(ctx context.Context, pageURL string) ([]paperlessDoc, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var page paginatedResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	nextURL := ""
	if page.Next != nil {
		nextURL = *page.Next
	}

	return page.Results, nextURL, nil
}

type lookupItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type lookupResponse struct {
	Results []lookupItem `json:"results"`
	Next    *string      `json:"next"`
}

func (c *Connector) fetchLookup(ctx context.Context, path string) (map[int]string, error) {
	lookup := make(map[int]string)
	fetchURL := c.baseURL + path + "?page_size=1000"

	for fetchURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Token "+c.token)

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() //nolint:errcheck // HTTP response body

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
		}

		var page lookupResponse
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, err
		}

		for _, item := range page.Results {
			lookup[item.ID] = item.Name
		}

		fetchURL = ""
		if page.Next != nil {
			fetchURL = *page.Next
		}
	}

	return lookup, nil
}

func (c *Connector) toDocument(pdoc paperlessDoc, tags, correspondents, docTypes map[int]string) model.Document {
	// Resolve tag names
	tagNames := make([]string, 0, len(pdoc.Tags))
	for _, tagID := range pdoc.Tags {
		if name, ok := tags[tagID]; ok {
			tagNames = append(tagNames, name)
		}
	}

	metadata := map[string]any{
		"original_file_name": pdoc.OriginalFileName,
		"tags":               tagNames,
		"added":              pdoc.Added.Format(time.RFC3339),
		"modified":           pdoc.Modified.Format(time.RFC3339),
	}

	if pdoc.Correspondent != nil {
		if name, ok := correspondents[*pdoc.Correspondent]; ok {
			metadata["correspondent"] = name
		}
	}
	if pdoc.DocumentType != nil {
		if name, ok := docTypes[*pdoc.DocumentType]; ok {
			metadata["document_type"] = name
		}
	}

	// Use document date (when originally issued), fall back to import date
	docDate := pdoc.Added
	if pdoc.Created != "" {
		if parsed, err := time.Parse("2006-01-02", pdoc.Created); err == nil {
			docDate = parsed
		}
	}

	sourceID := strconv.Itoa(pdoc.ID)
	return model.Document{
		ID:         model.DocumentID("paperless", c.name, sourceID),
		SourceType: "paperless",
		SourceName: c.name,
		SourceID:   sourceID,
		Title:      pdoc.Title,
		Content:    pdoc.Content,
		Metadata:   metadata,
		URL:        fmt.Sprintf("%s/documents/%d/details", c.baseURL, pdoc.ID),
		Visibility: "private",
		CreatedAt:  docDate,
	}
}
