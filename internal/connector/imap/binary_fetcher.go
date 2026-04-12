package imap

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/muty/nexus/internal/connector"
)

// sourceIDRe matches IMAP source ID formats emitted by messageToDocuments:
//
//	{folder}:{uid}                       — parent email
//	{folder}:{uid}:attachment:{idx}      — individual attachment
//
// The `.+?` is non-greedy so the regex prefers consuming the shortest
// prefix for the folder, letting the optional `:attachment:N` suffix
// match whenever present. Folder names containing colons still parse
// correctly because the required `:(\d+)` segment forces the engine
// to extend `.+?` past earlier colons that aren't followed by digits.
var sourceIDRe = regexp.MustCompile(`^(.+?):(\d+)(?::attachment:(\d+))?$`)

// parsedSourceID holds the components of a decoded IMAP source ID.
type parsedSourceID struct {
	folder        string
	uid           imap.UID
	attachmentIdx int
	isAttachment  bool
}

// parseSourceID decodes an IMAP source ID into its folder, UID, and
// optional attachment index.
func parseSourceID(s string) (parsedSourceID, error) {
	m := sourceIDRe.FindStringSubmatch(s)
	if m == nil {
		return parsedSourceID{}, fmt.Errorf("imap: invalid source id %q", s)
	}
	uid, err := strconv.ParseUint(m[2], 10, 32)
	if err != nil {
		return parsedSourceID{}, fmt.Errorf("imap: invalid uid in source id %q: %w", s, err)
	}
	out := parsedSourceID{
		folder: m[1],
		uid:    imap.UID(uid),
	}
	if m[3] != "" {
		idx, err := strconv.Atoi(m[3])
		if err != nil {
			return parsedSourceID{}, fmt.Errorf("imap: invalid attachment index in source id %q: %w", s, err)
		}
		out.attachmentIdx = idx
		out.isAttachment = true
	}
	return out, nil
}

// FetchBinary returns the bytes for an IMAP-indexed document. For
// attachment source IDs (`{folder}:{uid}:attachment:{idx}`) it returns
// that attachment's bytes with the original filename + content type.
// For parent email source IDs (`{folder}:{uid}`) it returns the raw
// RFC 5322 message as `message/rfc822`.
//
// Cache behavior follows c.cacheConfig.Mode:
//
//   - "none" or empty: bypass the cache entirely, always re-fetch.
//   - "lazy" (default for IMAP): check the cache first; on miss,
//     fetch from IMAP and populate the cache before returning.
//   - "eager": same cache-first behavior for FetchBinary; attachment
//     cache entries are additionally populated during Fetch (see
//     messageToDocuments) so most requests hit the cache with no IMAP
//     round-trip.
//
// Implements connector.BinaryFetcher.
func (c *Connector) FetchBinary(ctx context.Context, sourceID string) (*connector.BinaryContent, error) {
	parsed, err := parseSourceID(sourceID)
	if err != nil {
		return nil, err
	}

	// Cache-first for lazy/eager. On hit the caller gets a reader
	// backed by the local filesystem — no IMAP round-trip.
	if c.shouldUseCache() {
		if bc, err := c.serveFromCache(ctx, sourceID, parsed); err == nil {
			return bc, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			// Non-miss cache failures (DB down, file unreadable) log
			// and fall through to an IMAP fetch rather than erroring
			// the whole preview request.
			// No logger on the connector struct — fall through silently.
			_ = err
		}
	}

	// Cache miss (or mode=none): fetch the full message body from IMAP.
	body, err := c.fetchMessageBody(ctx, parsed.folder, parsed.uid)
	if err != nil {
		return nil, err
	}

	if parsed.isAttachment {
		att, err := attachmentByIndex(body, parsed.attachmentIdx)
		if err != nil {
			return nil, err
		}
		if c.shouldUseCache() && c.binaryStore != nil {
			// Best-effort populate — a cache write failure shouldn't
			// fail the preview request.
			_ = c.binaryStore.Put(ctx, "imap", c.name, sourceID, bytes.NewReader(att.Data), int64(len(att.Data)))
		}
		return &connector.BinaryContent{
			Reader:   io.NopCloser(bytes.NewReader(att.Data)),
			MimeType: att.ContentType,
			Size:     int64(len(att.Data)),
			Filename: att.Filename,
		}, nil
	}

	// Parent email: return the raw message as message/rfc822.
	if c.shouldUseCache() && c.binaryStore != nil {
		_ = c.binaryStore.Put(ctx, "imap", c.name, sourceID, bytes.NewReader(body), int64(len(body)))
	}
	return &connector.BinaryContent{
		Reader:   io.NopCloser(bytes.NewReader(body)),
		MimeType: "message/rfc822",
		Size:     int64(len(body)),
		Filename: fmt.Sprintf("%s-%d.eml", parsed.folder, parsed.uid),
	}, nil
}

// shouldUseCache reports whether the configured mode wants caching.
// Empty mode defaults to no caching so connectors without an injected
// store behave as if caching were disabled.
func (c *Connector) shouldUseCache() bool {
	if c.binaryStore == nil {
		return false
	}
	return c.cacheConfig.Mode == "lazy" || c.cacheConfig.Mode == "eager"
}

// serveFromCache attempts to build a BinaryContent from a cached blob.
// Returns os.ErrNotExist when the entry isn't cached. Callers should
// treat any non-nil error as a hint to fall through to the live
// source.
//
// Cached blobs carry no metadata of their own (mime type, filename),
// so we synthesize a sensible default: attachments get empty strings
// (the download handler falls back to chunk.Title for filename and
// application/octet-stream for mime), and parent emails always get
// message/rfc822 and a derived .eml filename.
func (c *Connector) serveFromCache(ctx context.Context, sourceID string, parsed parsedSourceID) (*connector.BinaryContent, error) {
	r, err := c.binaryStore.Get(ctx, "imap", c.name, sourceID)
	if err != nil {
		return nil, err
	}

	bc := &connector.BinaryContent{Reader: r}
	if !parsed.isAttachment {
		bc.MimeType = "message/rfc822"
		bc.Filename = fmt.Sprintf("%s-%d.eml", parsed.folder, parsed.uid)
	}
	// Size is unknown without an os.Stat on the underlying file; the
	// download handler only sets Content-Length when bc.Size > 0,
	// so leaving it zero is fine — clients still receive the bytes.
	return bc, nil
}

// fetchMessageBody dials IMAP, selects the folder, fetches the given
// UID's entire RFC 5322 body, and returns the raw bytes. Closes the
// connection before returning.
func (c *Connector) fetchMessageBody(ctx context.Context, folder string, uid imap.UID) ([]byte, error) {
	addr := fmt.Sprintf("%s:%d", c.server, c.port)
	client, err := c.dial(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: c.server},
	})
	if err != nil {
		return nil, fmt.Errorf("imap: connect: %w", err)
	}
	defer client.Close() //nolint:errcheck // best-effort

	if err := client.Login(c.username, c.password).Wait(); err != nil {
		return nil, fmt.Errorf("imap: login: %w", err)
	}
	defer func() { _ = client.Logout().Wait() }()

	if _, err := client.Select(folder, nil).Wait(); err != nil {
		return nil, fmt.Errorf("imap: select %q: %w", folder, err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	msgs, err := client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch uid %d: %w", uid, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("imap: message not found: %s:%d", folder, uid)
	}

	for _, section := range msgs[0].BodySection {
		return section.Bytes, nil
	}
	return nil, fmt.Errorf("imap: message %s:%d returned no body", folder, uid)
}

// attachmentByIndex parses an RFC 5322 message body and returns its
// Nth attachment. Mirrors the attachment enumeration in
// parseEmailBody so the index matches the one emitted by
// messageToDocuments at index time.
func attachmentByIndex(body []byte, idx int) (attachment, error) {
	_, attachments := parseEmailBody(body)
	if idx < 0 || idx >= len(attachments) {
		return attachment{}, fmt.Errorf("imap: attachment index %d out of range (message has %d)", idx, len(attachments))
	}
	return attachments[idx], nil
}
