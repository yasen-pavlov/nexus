package rag

import (
	"context"
	"io"
	"strings"

	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// maxImageBytes caps a single attached image. 5 MB keeps a 4-image turn
// well under every provider's per-request limit and stops a giant indexed
// image from blowing up token cost (master plan §8).
const maxImageBytes = 5 << 20

// attachImages fills Document.Images for the docs a vision-capable model
// can see, drawing from two sources in reranked order until the per-turn
// budget is spent:
//
//	A. a retrieved chunk that is ITSELF an image (mime image/*);
//	B. image attachments of a retrieved email / Telegram parent, found by
//	   walking the reverse `attachment_of` edge.
//
// Cache-only: a binary miss is skipped silently — never a synchronous
// refetch, which would add latency/cost on the hot answer path. Mutates
// docs in place; a no-op when deps are nil, the model can't see images,
// the admin disabled multimodal, or the budget is zero.
func (o *Orchestrator) attachImages(ctx context.Context, docs []llm.Document, hits []model.DocumentHit, info llm.ModelInfo, s Settings) {
	if o.binaries == nil || !info.SupportsVision || !s.EnableMultimodal {
		return
	}
	budget := s.MaxImagesPerTurn
	if budget <= 0 {
		return
	}

	// docs is the (possibly truncated) prefix of hits in the same order;
	// index by DocID so an attachment lands on the right Document.
	docByID := make(map[string]*llm.Document, len(docs))
	for i := range docs {
		docByID[docs[i].ID] = &docs[i]
	}

	// Pass A: retrieved image chunks. Stash non-image parents for pass B.
	var parentIDs, parentSourceIDs []string
	for _, h := range hits {
		if budget <= 0 {
			break
		}
		d := docByID[h.ID.String()]
		if d == nil {
			continue // hit beyond the doc cap — no Document to attach to
		}
		if isImageMime(h.MimeType) {
			if img, ok := loadCachedImage(ctx, o.binaries, h.SourceType, h.SourceName, h.SourceID, h.MimeType, h.ID.String()); ok {
				d.Images = append(d.Images, img)
				budget--
			}
			continue
		}
		parentIDs = append(parentIDs, h.ID.String())
		parentSourceIDs = append(parentSourceIDs, h.SourceID)
	}

	// Pass B: walk attachment_of for the non-image parents in one batched
	// query, then hang each image attachment off the parent it references.
	if budget <= 0 || o.attachments == nil || len(parentIDs) == 0 {
		return
	}
	atts, err := o.attachments.FindChunksReferencing(ctx, parentIDs, parentSourceIDs)
	if err != nil {
		o.log.Warn("rag: attachment edge walk failed", zap.Error(err))
		return
	}
	docBySourceID := make(map[string]*llm.Document, len(hits))
	for _, h := range hits {
		if d := docByID[h.ID.String()]; d != nil {
			docBySourceID[h.SourceID] = d
		}
	}
	for _, att := range atts {
		if budget <= 0 {
			break
		}
		if !isImageMime(att.MimeType) {
			continue
		}
		parent := attachmentParent(att, docByID, docBySourceID)
		if parent == nil {
			continue
		}
		if img, ok := loadCachedImage(ctx, o.binaries, att.SourceType, att.SourceName, att.SourceID, att.MimeType, att.ID); ok {
			parent.Images = append(parent.Images, img)
			budget--
		}
	}
}

// attachmentParent resolves the Document an attachment chunk hangs off by
// matching its attachment_of relation target against the retrieved docs
// (by doc id first, then by source id).
func attachmentParent(att model.Chunk, byID, bySourceID map[string]*llm.Document) *llm.Document {
	for _, r := range att.Relations {
		if r.Type != model.RelationAttachmentOf {
			continue
		}
		if r.TargetID != "" {
			if d := byID[r.TargetID]; d != nil {
				return d
			}
		}
		if r.TargetSourceID != "" {
			if d := bySourceID[r.TargetSourceID]; d != nil {
				return d
			}
		}
	}
	return nil
}

// loadCachedImage reads a cached binary (cache-only) and wraps it as an
// llm.Image, enforcing the per-image size cap. Returns ok=false on a nil
// store, cache miss, oversize, or read error so the caller silently skips
// it. Shared by the auto-attachment pass and the nexus_open_attachment
// tool dispatcher.
func loadCachedImage(ctx context.Context, store ImageStore, sourceType, sourceName, sourceID, mime, citeID string) (llm.Image, bool) {
	if store == nil {
		return llm.Image{}, false
	}
	rc, err := store.Get(ctx, sourceType, sourceName, sourceID)
	if err != nil {
		return llm.Image{}, false
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return llm.Image{}, false
	}
	return llm.Image{MediaType: mime, Data: data, SourceID: citeID}, true
}

// isImageMime reports whether a mime type is an image/* type.
func isImageMime(mime string) bool {
	return strings.HasPrefix(strings.ToLower(mime), "image/")
}
