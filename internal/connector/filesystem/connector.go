// Package filesystem implements a connector that crawls local directories for text files.
package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mime"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
)

func init() {
	connector.Register("filesystem", func() connector.Connector {
		return &Connector{}
	})
}

// Connector crawls local directories and extracts text from files.
type Connector struct {
	name      string
	rootPath  string
	patterns  []string
	syncSince time.Time
	extractor *extractor.Registry
}

// SetExtractor sets the content extractor for non-text files.
func (c *Connector) SetExtractor(ext *extractor.Registry) {
	c.extractor = ext
}

func (c *Connector) Type() string { return "filesystem" }
func (c *Connector) Name() string { return c.name }

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "filesystem"
	}
	c.name = name

	rootPath, _ := cfg["root_path"].(string)
	if rootPath == "" {
		return fmt.Errorf("filesystem: root_path is required")
	}
	c.rootPath = rootPath

	patterns, _ := cfg["patterns"].(string)
	if patterns == "" {
		patterns = "*.txt,*.md"
	}
	c.patterns = strings.Split(patterns, ",")
	for i := range c.patterns {
		c.patterns[i] = strings.TrimSpace(c.patterns[i])
	}

	c.syncSince = connector.ComputeSyncSince(cfg)

	return nil
}

func (c *Connector) Validate() error {
	info, err := os.Stat(c.rootPath)
	if err != nil {
		return fmt.Errorf("filesystem: root_path %q: %w", c.rootPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("filesystem: root_path %q is not a directory", c.rootPath)
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, cursor *model.SyncCursor) (*model.FetchResult, error) {
	var lastSync time.Time
	if cursor != nil {
		if ts, ok := cursor.CursorData["last_sync_time"].(string); ok {
			lastSync, _ = time.Parse(time.RFC3339Nano, ts)
		}
	} else if !c.syncSince.IsZero() {
		lastSync = c.syncSince
	}

	var docs []model.Document
	err := filepath.WalkDir(c.rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}

		if !c.matchesPattern(d.Name()) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil // skip files we can't stat
		}

		// Incremental sync: skip files not modified since last sync
		if !lastSync.IsZero() && info.ModTime().Before(lastSync) {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return nil // skip files we can't read
		}

		relPath, _ := filepath.Rel(c.rootPath, path)
		contentType := detectContentType(d.Name())

		// Extract text content
		var textContent string
		if c.extractor != nil && c.extractor.CanExtract(contentType) {
			extracted, err := c.extractor.Extract(ctx, contentType, raw)
			if err != nil {
				return nil // skip files we can't extract
			}
			textContent = extracted
		} else {
			textContent = string(raw)
		}

		docs = append(docs, model.Document{
			ID:         model.DocumentID("filesystem", c.name, relPath),
			SourceType: "filesystem",
			SourceName: c.name,
			SourceID:   relPath,
			Title:      d.Name(),
			Content:    textContent,
			Metadata: map[string]any{
				"path":         relPath,
				"size":         info.Size(),
				"extension":    filepath.Ext(d.Name()),
				"content_type": contentType,
			},
			URL:        "file://" + path,
			Visibility: "private",
			CreatedAt:  info.ModTime(),
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("filesystem: walk: %w", err)
	}

	now := time.Now()
	return &model.FetchResult{
		Documents: docs,
		Cursor: &model.SyncCursor{
			CursorData: map[string]any{
				"last_sync_time": now.Format(time.RFC3339Nano),
			},
			LastSync:    now,
			LastStatus:  "success",
			ItemsSynced: len(docs),
		},
	}, nil
}

func (c *Connector) matchesPattern(name string) bool {
	for _, pattern := range c.patterns {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func detectContentType(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return "application/octet-stream"
	}
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}
