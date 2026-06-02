package rag

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// fakeImageStore is an in-memory ImageStore keyed by the binary triple.
type fakeImageStore struct {
	blobs map[string][]byte
	err   error
}

func key(st, sn, sid string) string { return st + "|" + sn + "|" + sid }

func (f *fakeImageStore) Get(_ context.Context, st, sn, sid string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.blobs[key(st, sn, sid)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// fakeAttachmentResolver fakes both relation-walk and id lookup.
type fakeAttachmentResolver struct {
	referencing []model.Chunk
	byID        map[string]*model.Chunk
	refErr      error
}

func (f *fakeAttachmentResolver) FindChunksReferencing(_ context.Context, _, _ []string) ([]model.Chunk, error) {
	return f.referencing, f.refErr
}

func (f *fakeAttachmentResolver) GetChunkByDocID(_ context.Context, docID string) (*model.Chunk, error) {
	if c, ok := f.byID[docID]; ok {
		return c, nil
	}
	return nil, errors.New("not found")
}

func imageHit(id uuid.UUID, mime string) model.DocumentHit {
	return model.DocumentHit{Document: model.Document{
		ID: id, Title: "pic", SourceType: "imap", SourceName: "mail", SourceID: "INBOX:1:att:0", MimeType: mime,
	}}
}

func TestAttachImages_RetrievedImageChunk(t *testing.T) {
	id := uuid.New()
	hits := []model.DocumentHit{imageHit(id, "image/png")}
	docs := buildLLMDocs(hits, 10)
	fis := &fakeImageStore{blobs: map[string][]byte{key("imap", "mail", "INBOX:1:att:0"): []byte("PNGDATA")}}
	o := &Orchestrator{binaries: fis, log: zap.NewNop()}

	o.attachImages(context.Background(), docs, hits, llm.ModelInfo{SupportsVision: true},
		Settings{EnableMultimodal: true, MaxImagesPerTurn: 4})

	if len(docs[0].Images) != 1 {
		t.Fatalf("Images = %d, want 1", len(docs[0].Images))
	}
	if docs[0].Images[0].MediaType != "image/png" || string(docs[0].Images[0].Data) != "PNGDATA" {
		t.Errorf("image payload wrong: %+v", docs[0].Images[0])
	}
}

func TestAttachImages_SkipWhenNoVisionOrDisabled(t *testing.T) {
	id := uuid.New()
	hits := []model.DocumentHit{imageHit(id, "image/png")}
	fis := &fakeImageStore{blobs: map[string][]byte{key("imap", "mail", "INBOX:1:att:0"): []byte("x")}}
	o := &Orchestrator{binaries: fis, log: zap.NewNop()}

	cases := []struct {
		name string
		info llm.ModelInfo
		s    Settings
	}{
		{"no vision", llm.ModelInfo{SupportsVision: false}, Settings{EnableMultimodal: true, MaxImagesPerTurn: 4}},
		{"multimodal off", llm.ModelInfo{SupportsVision: true}, Settings{EnableMultimodal: false, MaxImagesPerTurn: 4}},
		{"zero budget", llm.ModelInfo{SupportsVision: true}, Settings{EnableMultimodal: true, MaxImagesPerTurn: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs := buildLLMDocs(hits, 10)
			o.attachImages(context.Background(), docs, hits, tc.info, tc.s)
			if len(docs[0].Images) != 0 {
				t.Errorf("Images = %d, want 0", len(docs[0].Images))
			}
		})
	}
}

func TestAttachImages_BudgetCap(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	hits := []model.DocumentHit{
		{Document: model.Document{ID: a, SourceType: "imap", SourceName: "m", SourceID: "s1", MimeType: "image/png"}},
		{Document: model.Document{ID: b, SourceType: "imap", SourceName: "m", SourceID: "s2", MimeType: "image/png"}},
	}
	docs := buildLLMDocs(hits, 10)
	fis := &fakeImageStore{blobs: map[string][]byte{
		key("imap", "m", "s1"): []byte("1"),
		key("imap", "m", "s2"): []byte("2"),
	}}
	o := &Orchestrator{binaries: fis, log: zap.NewNop()}
	o.attachImages(context.Background(), docs, hits, llm.ModelInfo{SupportsVision: true},
		Settings{EnableMultimodal: true, MaxImagesPerTurn: 1})

	total := len(docs[0].Images) + len(docs[1].Images)
	if total != 1 {
		t.Errorf("attached %d images, want 1 (budget cap)", total)
	}
}

func TestAttachImages_SkipsOversizeAndCacheMiss(t *testing.T) {
	big, miss := uuid.New(), uuid.New()
	hits := []model.DocumentHit{
		{Document: model.Document{ID: big, SourceType: "imap", SourceName: "m", SourceID: "big", MimeType: "image/png"}},
		{Document: model.Document{ID: miss, SourceType: "imap", SourceName: "m", SourceID: "miss", MimeType: "image/png"}},
	}
	docs := buildLLMDocs(hits, 10)
	fis := &fakeImageStore{blobs: map[string][]byte{
		key("imap", "m", "big"): make([]byte, maxImageBytes+1), // over cap
		// "miss" intentionally absent → cache miss
	}}
	o := &Orchestrator{binaries: fis, log: zap.NewNop()}
	o.attachImages(context.Background(), docs, hits, llm.ModelInfo{SupportsVision: true},
		Settings{EnableMultimodal: true, MaxImagesPerTurn: 4})

	if len(docs[0].Images) != 0 || len(docs[1].Images) != 0 {
		t.Errorf("oversize/miss should attach nothing, got %d/%d", len(docs[0].Images), len(docs[1].Images))
	}
}

func TestAttachImages_WalksAttachmentEdge(t *testing.T) {
	emailID := uuid.New()
	hits := []model.DocumentHit{{Document: model.Document{
		ID: emailID, Title: "Email with photo", SourceType: "imap", SourceName: "mail", SourceID: "INBOX:9", MimeType: "message/rfc822",
	}}}
	docs := buildLLMDocs(hits, 10)

	att := model.Chunk{
		ID: uuid.New().String(), SourceType: "imap", SourceName: "mail", SourceID: "INBOX:9:att:0", MimeType: "image/jpeg",
		Relations: []model.Relation{{Type: model.RelationAttachmentOf, TargetID: emailID.String(), TargetSourceID: "INBOX:9"}},
	}
	far := &fakeAttachmentResolver{referencing: []model.Chunk{att}}
	fis := &fakeImageStore{blobs: map[string][]byte{key("imap", "mail", "INBOX:9:att:0"): []byte("JPEG")}}
	o := &Orchestrator{binaries: fis, attachments: far, log: zap.NewNop()}

	o.attachImages(context.Background(), docs, hits, llm.ModelInfo{SupportsVision: true},
		Settings{EnableMultimodal: true, MaxImagesPerTurn: 4})

	if len(docs[0].Images) != 1 {
		t.Fatalf("walked attachment image not attached to parent: %d", len(docs[0].Images))
	}
	if string(docs[0].Images[0].Data) != "JPEG" {
		t.Errorf("walked image payload wrong: %q", docs[0].Images[0].Data)
	}
}

func TestIsImageMime(t *testing.T) {
	for _, m := range []string{"image/png", "IMAGE/JPEG", "image/webp"} {
		if !isImageMime(m) {
			t.Errorf("isImageMime(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "application/pdf", "text/plain", "video/mp4"} {
		if isImageMime(m) {
			t.Errorf("isImageMime(%q) = true, want false", m)
		}
	}
}

// --- nexus_open_attachment dispatch ---

func openAttachCall(chunkID string) llm.ToolCall {
	return llm.ToolCall{Name: nexusOpenAttachmentToolName, ArgsJSON: `{"chunk_id":"` + chunkID + `"}`}
}

func TestBuildToolList_OpenAttachmentFlag(t *testing.T) {
	info := llm.ModelInfo{SupportsTools: true}
	if got := BuildToolList(info, 3, false); len(got) != 1 {
		t.Errorf("flag off: got %d tools, want 1 (search only)", len(got))
	}
	got := BuildToolList(info, 3, true)
	if len(got) != 2 || got[1].Name != nexusOpenAttachmentToolName {
		t.Errorf("flag on: got %v, want search + open_attachment", got)
	}
}

func TestOpenAttachment_ImageHappyPath(t *testing.T) {
	owner := uuid.New()
	id := uuid.New().String()
	far := &fakeAttachmentResolver{byID: map[string]*model.Chunk{id: {
		ID: id, Title: "diagram.png", SourceType: "imap", SourceName: "mail", SourceID: "INBOX:1:att:0",
		MimeType: "image/png", OwnerID: owner.String(),
	}}}
	fis := &fakeImageStore{blobs: map[string][]byte{key("imap", "mail", "INBOX:1:att:0"): []byte("PNG")}}
	d := newSearchToolDispatcher(nil, far, fis, owner, true, zap.NewNop())

	out := d.Dispatch(context.Background(), openAttachCall(id))
	if len(out.Docs) != 1 || len(out.Docs[0].Images) != 1 {
		t.Fatalf("want 1 doc with 1 image, got %d docs", len(out.Docs))
	}
	if !strings.Contains(out.ResultText, "image attached") {
		t.Errorf("ResultText = %q", out.ResultText)
	}
	if len(out.Chunks) != 1 || out.Chunks[0].MimeType != "image/png" {
		t.Errorf("preview chunk wrong: %+v", out.Chunks)
	}
	// Doc + preview must carry the caller-supplied UUID (resolves via
	// /api/documents/{id}/content for the FE thumbnail), not the composite
	// chunk._id.
	if out.Docs[0].ID != id || out.Chunks[0].DocID != id {
		t.Errorf("ids = doc %q / preview %q, want %q", out.Docs[0].ID, out.Chunks[0].DocID, id)
	}
}

func TestOpenAttachment_NonVisionReturnsText(t *testing.T) {
	owner := uuid.New()
	id := uuid.New().String()
	far := &fakeAttachmentResolver{byID: map[string]*model.Chunk{id: {
		ID: id, Title: "notes.pdf", SourceType: "paperless", SourceName: "p", SourceID: "42",
		MimeType: "application/pdf", Content: "quarterly revenue up 12%", OwnerID: owner.String(),
	}}}
	d := newSearchToolDispatcher(nil, far, &fakeImageStore{}, owner, false, zap.NewNop())

	out := d.Dispatch(context.Background(), openAttachCall(id))
	if len(out.Docs) != 1 || len(out.Docs[0].Images) != 0 {
		t.Fatalf("non-vision should attach no image, got %d images", len(out.Docs[0].Images))
	}
	if !strings.Contains(out.ResultText, "quarterly revenue") {
		t.Errorf("ResultText should carry extracted text: %q", out.ResultText)
	}
}

func TestOpenAttachment_OwnershipDenied(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	id := uuid.New().String()
	far := &fakeAttachmentResolver{byID: map[string]*model.Chunk{id: {
		ID: id, Title: "secret.png", SourceType: "imap", MimeType: "image/png", OwnerID: other.String(), Shared: false,
	}}}
	d := newSearchToolDispatcher(nil, far, &fakeImageStore{}, owner, true, zap.NewNop())

	out := d.Dispatch(context.Background(), openAttachCall(id))
	if len(out.Docs) != 0 {
		t.Errorf("cross-user attachment leaked: %d docs", len(out.Docs))
	}
	if !strings.Contains(out.ResultText, "no attachment found") {
		t.Errorf("ResultText = %q, want not-found-style message", out.ResultText)
	}
}

func TestOpenAttachment_SharedReadableByNonOwner(t *testing.T) {
	owner := uuid.New()
	id := uuid.New().String()
	far := &fakeAttachmentResolver{byID: map[string]*model.Chunk{id: {
		ID: id, Title: "shared.txt", SourceType: "filesystem", Content: "hello", OwnerID: uuid.New().String(), Shared: true,
	}}}
	d := newSearchToolDispatcher(nil, far, &fakeImageStore{}, owner, false, zap.NewNop())

	out := d.Dispatch(context.Background(), openAttachCall(id))
	if len(out.Docs) != 1 {
		t.Errorf("shared doc should be readable, got %d docs", len(out.Docs))
	}
}

func TestOpenAttachment_NotFoundAndBadArgs(t *testing.T) {
	owner := uuid.New()
	far := &fakeAttachmentResolver{byID: map[string]*model.Chunk{}}
	d := newSearchToolDispatcher(nil, far, &fakeImageStore{}, owner, true, zap.NewNop())

	if out := d.Dispatch(context.Background(), openAttachCall("ghost")); !strings.Contains(out.ResultText, "no attachment found") {
		t.Errorf("missing chunk: %q", out.ResultText)
	}
	if out := d.Dispatch(context.Background(), llm.ToolCall{Name: nexusOpenAttachmentToolName, ArgsJSON: `{"chunk_id":"  "}`}); !strings.Contains(out.ResultText, "required") {
		t.Errorf("empty id: %q", out.ResultText)
	}
	if out := d.Dispatch(context.Background(), llm.ToolCall{Name: nexusOpenAttachmentToolName, ArgsJSON: `{bad`}); !strings.Contains(out.ResultText, "invalid arguments") {
		t.Errorf("malformed: %q", out.ResultText)
	}
}

func TestOpenAttachment_UnavailableWhenNoResolver(t *testing.T) {
	d := newSearchToolDispatcher(nil, nil, nil, uuid.New(), true, zap.NewNop())
	out := d.Dispatch(context.Background(), openAttachCall("x"))
	if !strings.Contains(out.ResultText, "not available") {
		t.Errorf("ResultText = %q, want unavailable", out.ResultText)
	}
}
