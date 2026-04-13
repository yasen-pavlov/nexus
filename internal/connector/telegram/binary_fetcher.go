package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/muty/nexus/internal/connector"
)

// mediaSourceIDRe matches the Telegram media source ID format emitted
// by mediaToDocument:
//
//	{chatID}:{msgID}:media
//
// The non-greedy `.+?` mirrors the IMAP regex shape and would handle
// chat IDs containing colons if that ever became a thing; today chat
// IDs are integers so the form is academic.
var mediaSourceIDRe = regexp.MustCompile(`^(.+?):(\d+):media$`)

// FetchBinary returns the cached bytes for a Telegram-indexed media
// document. Cache-only by design: Telegram file references expire,
// so there's no general-purpose way to re-download on miss. A miss
// surfaces a clear error — the operator must re-sync to repopulate.
//
// Implements connector.BinaryFetcher.
func (c *Connector) FetchBinary(ctx context.Context, sourceID string) (*connector.BinaryContent, error) {
	if !mediaSourceIDRe.MatchString(sourceID) {
		return nil, fmt.Errorf("telegram: invalid media source id %q", sourceID)
	}

	if c.binaryStore == nil || c.cacheConfig.Mode == "none" || c.cacheConfig.Mode == "" {
		return nil, fmt.Errorf("telegram: binary cache disabled — cannot serve %s", sourceID)
	}

	r, err := c.binaryStore.Get(ctx, "telegram", c.name, sourceID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("telegram: media not cached for %s (file references expire — re-sync the connector to repopulate)", sourceID)
		}
		return nil, fmt.Errorf("telegram: cache read: %w", err)
	}

	// Cached blobs carry no sidecar metadata. Leave MimeType/Filename
	// empty so the download handler falls back to chunk.Title +
	// application/octet-stream — same convention as IMAP attachments.
	return &connector.BinaryContent{Reader: r}, nil
}
