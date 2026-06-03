package rag

import (
	"context"
	"io"
	"strings"

	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// maxAttachmentBytes caps a single attached image or PDF. 5 MB keeps a
// 4-attachment turn well under every provider's per-request limit and
// stops a giant indexed file from blowing up token cost (master plan §8).
const maxAttachmentBytes = 5 << 20

// attachMedia fills Document.Images and Document.PDFs for a model that can
// consume them, drawing from two sources in reranked order until the
// per-turn budget is spent:
//
//	A. a retrieved chunk that is itself an image or a PDF;
//	B. image/PDF attachments of a retrieved email / Telegram parent, found
//	   by walking the reverse `attachment_of` edge.
//
// Images go to vision models (SupportsVision); PDFs go to models with
// native PDF support (SupportsPDF — Anthropic + OpenAI). The budget is
// shared across both kinds. Cache-only: a binary miss is skipped silently
// — never a synchronous refetch on the answer path. Mutates docs in place;
// a no-op when deps are nil, multimodal is disabled, the model can take
// neither kind, or the budget is zero.
func (o *Orchestrator) attachMedia(ctx context.Context, docs []llm.Document, hits []model.DocumentHit, info llm.ModelInfo, s Settings) {
	canImg := info.SupportsVision
	canPDF := info.SupportsPDF
	if o.binaries == nil || !s.EnableMultimodal || (!canImg && !canPDF) {
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

	// Pass A: retrieved media chunks. Stash non-media parents for pass B.
	var parentIDs, parentSourceIDs []string
	for _, h := range hits {
		if budget <= 0 {
			break
		}
		if !isAttachableMedia(h.MimeType) {
			parentIDs = append(parentIDs, h.ID.String())
			parentSourceIDs = append(parentSourceIDs, h.SourceID)
			continue
		}
		if d := docByID[h.ID.String()]; d != nil {
			if o.attachOne(ctx, d, h.SourceType, h.SourceName, h.SourceID, h.MimeType, h.ID.String(), h.Title, canImg, canPDF) {
				budget--
			}
		}
	}

	// Pass B: walk attachment_of for the non-media parents in one batched
	// query, then hang each media attachment off the parent it references.
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
		if !isAttachableMedia(att.MimeType) {
			continue
		}
		parent := attachmentParent(att, docByID, docBySourceID)
		if parent == nil {
			continue
		}
		if o.attachOne(ctx, parent, att.SourceType, att.SourceName, att.SourceID, att.MimeType, att.ID, att.Title, canImg, canPDF) {
			budget--
		}
	}
}

// attachOne loads one cached binary and appends it to the doc as the right
// payload kind (image for vision models, PDF for native-PDF models).
// Returns true when it attached something (and thus consumed budget).
func (o *Orchestrator) attachOne(ctx context.Context, d *llm.Document, sourceType, sourceName, sourceID, mime, citeID, filename string, canImg, canPDF bool) bool {
	switch {
	case canImg && isImageMime(mime):
		if img, ok := loadCachedImage(ctx, o.binaries, sourceType, sourceName, sourceID, mime, citeID); ok {
			d.Images = append(d.Images, img)
			return true
		}
	case canPDF && isPDFMime(mime):
		if pdf, ok := loadCachedPDF(ctx, o.binaries, sourceType, sourceName, sourceID, filename, citeID); ok {
			d.PDFs = append(d.PDFs, pdf)
			return true
		}
	}
	return false
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

// loadCachedBytes reads a cached binary (cache-only) enforcing the size
// cap. Returns ok=false on a nil store, cache miss, oversize, or read
// error so the caller silently skips it.
func loadCachedBytes(ctx context.Context, store ImageStore, sourceType, sourceName, sourceID string) ([]byte, bool) {
	if store == nil {
		return nil, false
	}
	rc, err := store.Get(ctx, sourceType, sourceName, sourceID)
	if err != nil {
		return nil, false
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxAttachmentBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxAttachmentBytes {
		return nil, false
	}
	return data, true
}

// loadCachedImage wraps loadCachedBytes as an llm.Image. Shared by the
// auto-attach pass and the nexus_open_attachment tool dispatcher.
func loadCachedImage(ctx context.Context, store ImageStore, sourceType, sourceName, sourceID, mime, citeID string) (llm.Image, bool) {
	data, ok := loadCachedBytes(ctx, store, sourceType, sourceName, sourceID)
	if !ok {
		return llm.Image{}, false
	}
	return llm.Image{MediaType: mime, Data: data, SourceID: citeID}, true
}

// loadCachedPDF wraps loadCachedBytes as an llm.PDF.
func loadCachedPDF(ctx context.Context, store ImageStore, sourceType, sourceName, sourceID, filename, citeID string) (llm.PDF, bool) {
	data, ok := loadCachedBytes(ctx, store, sourceType, sourceName, sourceID)
	if !ok {
		return llm.PDF{}, false
	}
	return llm.PDF{MediaType: "application/pdf", Data: data, Filename: filename, SourceID: citeID}, true
}

// isImageMime reports whether a mime type is an image/* type.
func isImageMime(mime string) bool {
	return strings.HasPrefix(strings.ToLower(mime), "image/")
}

// isPDFMime reports whether a mime type is application/pdf.
func isPDFMime(mime string) bool {
	return strings.HasPrefix(strings.ToLower(mime), "application/pdf")
}

// isAttachableMedia reports whether a mime is something we can attach to a
// capable model (image or PDF) — used to decide direct-attach vs. treat
// the chunk as a parent to walk for attachments.
func isAttachableMedia(mime string) bool {
	return isImageMime(mime) || isPDFMime(mime)
}
