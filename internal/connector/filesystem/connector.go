// Package filesystem implements a connector that crawls local directories for text files.
package filesystem

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
)

// filesystemCheckpointEvery is how often the connector emits a
// cursor checkpoint during a WalkDir pass. Matches the pipeline's
// indexBatchSize so a checkpoint coincides with a bulk-index flush.
const filesystemCheckpointEvery = 200

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

// Fetch streams filesystem entries as a single WalkDir pass. For each
// matching file the connector emits a SourceID (the relative path,
// regardless of whether the file is re-indexed this run) for the
// pipeline's deletion reconciliation, followed by a Doc when the file
// has been modified since the cursor's last_sync_time.
//
// WalkDir visits entries in lexical order per directory; the resulting
// relative paths match OpenSearch's source_id.keyword sort closely
// enough for the streaming merge-diff — Go's WalkDir promises lexical
// order and the merge-diff treats any out-of-order emission as a
// false-positive "delete", but filesystem paths have no numeric
// collation hazards like IMAP UIDs, so the natural order works.
func (c *Connector) Fetch(ctx context.Context, cursor *model.SyncCursor) (<-chan model.FetchItem, <-chan error) {
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)

	go func() {
		defer close(items)
		defer close(errs)
		if err := c.streamWalk(ctx, cursor, items); err != nil {
			errs <- err
		}
	}()

	return items, errs
}

// streamWalk does the single WalkDir pass, emitting SourceID / Doc /
// Checkpoint items via the provided channel. Extracted from Fetch so
// each piece of the walk — entry classification, periodic
// checkpoint, terminal markers — is its own tightly-scoped helper.
func (c *Connector) streamWalk(ctx context.Context, cursor *model.SyncCursor, items chan<- model.FetchItem) error {
	lastSync := resolveFilesystemCursor(cursor, c.syncSince)
	now := time.Now()
	seen := 0

	walkErr := filepath.WalkDir(c.rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.emitWalkEntry(ctx, path, d, lastSync, now, &seen, items)
	})
	if walkErr != nil {
		return fmt.Errorf("filesystem: walk: %w", walkErr)
	}

	// Signal that the SourceID stream was authoritative. Empty
	// directories (zero SourceID emissions) rely on this to wipe
	// any leftover index entries rather than being treated as
	// "opted out of reconciliation."
	_ = fsEmit(ctx, items, model.FetchItem{EnumerationComplete: true})
	// Final checkpoint so the pipeline persists last_sync_time
	// even on a walk that happened to land exactly on an
	// every-N boundary (or a walk with < filesystemCheckpointEvery
	// files).
	_ = fsEmit(ctx, items, model.FetchItem{Checkpoint: newFilesystemCursor(now)})
	return nil
}

// emitWalkEntry runs the per-file emission sequence: SourceID
// (always when the file matches the pattern), Doc (when the file's
// content is fresh relative to lastSync), and a periodic Checkpoint
// on every N-th emission. seen is incremented via pointer so the
// checkpoint cadence survives across WalkDir callback invocations.
func (c *Connector) emitWalkEntry(ctx context.Context, path string, d os.DirEntry, lastSync, startedAt time.Time, seen *int, items chan<- model.FetchItem) error {
	doc, relPath, ok := c.processWalkEntry(ctx, path, d, lastSync)
	if relPath != "" {
		sid := relPath
		if !fsEmit(ctx, items, model.FetchItem{SourceID: &sid}) {
			return ctx.Err()
		}
		*seen++
	}
	if ok {
		if !fsEmit(ctx, items, model.FetchItem{Doc: &doc}) {
			return ctx.Err()
		}
	}
	if *seen > 0 && *seen%filesystemCheckpointEvery == 0 {
		if !fsEmit(ctx, items, model.FetchItem{Checkpoint: newFilesystemCursor(startedAt)}) {
			return ctx.Err()
		}
	}
	return nil
}

// fsEmit sends an item on items, respecting context cancellation.
// Matches the emitItem helper in the other connectors so cancellation
// semantics stay uniform across the codebase.
func fsEmit(ctx context.Context, items chan<- model.FetchItem, item model.FetchItem) bool {
	select {
	case items <- item:
		return true
	case <-ctx.Done():
		return false
	}
}

// newFilesystemCursor builds a cursor payload with the run's start
// timestamp. Time is captured once at the top of Fetch so every
// checkpoint during a run persists the same last_sync_time — a later
// incremental run picks up any file with ModTime >= that value.
func newFilesystemCursor(startedAt time.Time) *model.SyncCursor {
	return &model.SyncCursor{
		CursorData: map[string]any{
			"last_sync_time": startedAt.Format(time.RFC3339Nano),
		},
		LastSync:   startedAt,
		LastStatus: "success",
	}
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
