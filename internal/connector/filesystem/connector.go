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
	lastSync := resolveFilesystemCursor(cursor, c.syncSince)

	var docs []model.Document
	// Every matching file's source_id, regardless of the lastSync filter.
	// Populated during the same walk so deletion sync can diff this
	// authoritative list against what's indexed. Built in parallel with
	// docs so a single walk covers both incremental indexing and full
	// enumeration.
	currentSourceIDs := []string{}
	err := filepath.WalkDir(c.rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		doc, relPath, ok := c.processWalkEntry(ctx, path, d, lastSync)
		if relPath != "" {
			currentSourceIDs = append(currentSourceIDs, relPath)
		}
		if ok {
			docs = append(docs, doc)
		}
		return nil
	})
	if err != nil {
		// Walk errored partway → enumeration is incomplete. Return the
		// docs we did collect (so indexing of the readable subset still
		// happens) but null out CurrentSourceIDs to skip the deletion
		// pass — a partial list would trigger false-positive deletions.
		return nil, fmt.Errorf("filesystem: walk: %w", err)
	}

	now := time.Now()
	return &model.FetchResult{
		Documents:        docs,
		CurrentSourceIDs: currentSourceIDs,
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

// resolveFilesystemCursor reads the persisted last_sync_time out of the
// cursor; falls back to the connector's syncSince cutoff on first run.
func resolveFilesystemCursor(cursor *model.SyncCursor, syncSince time.Time) time.Time {
	if cursor != nil {
		if ts, ok := cursor.CursorData["last_sync_time"].(string); ok {
			t, _ := time.Parse(time.RFC3339Nano, ts)
			return t
		}
		return time.Time{}
	}
	if !syncSince.IsZero() {
		return syncSince
	}
	return time.Time{}
}

// processWalkEntry inspects a single filesystem entry during Fetch's WalkDir.
// Returns (doc, relPath, emit). relPath is non-empty for every enumerated
// file (regardless of whether it was re-indexed this run) so deletion sync
// gets the authoritative set; emit is true only when a Document should be
// indexed.
func (c *Connector) processWalkEntry(ctx context.Context, path string, d os.DirEntry, lastSync time.Time) (model.Document, string, bool) {
	if d.IsDir() || !c.matchesPattern(d.Name()) {
		return model.Document{}, "", false
	}
	info, err := d.Info()
	if err != nil {
		return model.Document{}, "", false // skip files we can't stat
	}
	relPath, _ := filepath.Rel(c.rootPath, path)
	// Incremental sync: skip files not modified since last sync, but still
	// record their source_id for deletion reconciliation above.
	if !lastSync.IsZero() && info.ModTime().Before(lastSync) {
		return model.Document{}, relPath, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return model.Document{}, relPath, false // skip files we can't read
	}
	contentType := detectContentType(d.Name())
	textContent := ""
	// Try extraction. On failure or unsupported type, the doc is still
	// emitted with empty content so it remains discoverable by metadata
	// (filename, type) and previewable via the BinaryFetcher path.
	if c.extractor != nil && c.extractor.CanExtract(contentType) {
		if extracted, err := c.extractor.Extract(ctx, contentType, raw); err == nil {
			textContent = extracted
		}
	}
	doc := model.Document{
		ID:         model.DocumentID("filesystem", c.name, relPath),
		SourceType: "filesystem",
		SourceName: c.name,
		SourceID:   relPath,
		Title:      d.Name(),
		Content:    textContent,
		MimeType:   contentType,
		Size:       info.Size(),
		Metadata: map[string]any{
			"path":         relPath,
			"size":         info.Size(),
			"extension":    filepath.Ext(d.Name()),
			"content_type": contentType,
		},
		URL:        "file://" + path,
		Visibility: "private",
		CreatedAt:  info.ModTime(),
	}
	return doc, relPath, true
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

// FetchBinary implements connector.BinaryFetcher. It opens the file identified
// by sourceID (a path relative to the connector's root) and returns it as a
// streaming reader. Path traversal is prevented by resolving symlinks and
// verifying the result is still under the configured root.
func (c *Connector) FetchBinary(_ context.Context, sourceID string) (*connector.BinaryContent, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("filesystem: empty source id")
	}

	// Resolve the absolute, symlink-free root once so we have a stable prefix
	// to compare against. We do the same for the requested file. If either
	// EvalSymlinks fails (e.g. broken link), we refuse to serve the file rather
	// than fall back to a less safe check.
	rootAbs, err := filepath.EvalSymlinks(c.rootPath)
	if err != nil {
		return nil, fmt.Errorf("filesystem: resolve root: %w", err)
	}

	requested := filepath.Join(rootAbs, filepath.Clean("/"+sourceID))
	resolved, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return nil, fmt.Errorf("filesystem: resolve file: %w", err)
	}

	rel, err := filepath.Rel(rootAbs, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("filesystem: path %q escapes root", sourceID)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("filesystem: stat: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("filesystem: %q is a directory", sourceID)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("filesystem: open: %w", err)
	}

	return &connector.BinaryContent{
		Reader:   f,
		MimeType: detectContentType(filepath.Base(resolved)),
		Size:     info.Size(),
		Filename: filepath.Base(resolved),
	}, nil
}
