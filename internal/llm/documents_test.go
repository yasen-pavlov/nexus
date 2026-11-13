package llm

import "testing"

func docIDs(docs []Document) []string {
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids
}

func assertIDs(t *testing.T, got []Document, want ...string) {
	t.Helper()
	ids := docIDs(got)
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

func TestMergeDocuments_AppendsNewIDsInOrder(t *testing.T) {
	got := MergeDocuments([]Document{{ID: "x"}, {ID: "y"}}, []Document{{ID: "y"}, {ID: "z"}})
	assertIDs(t, got, "x", "y", "z")
}

func TestMergeDocuments_MergesMediaOntoExisting(t *testing.T) {
	// The open_attachment case: "y" is already present as text-only, and the
	// incoming duplicate carries an image. The media must be merged onto the
	// existing entry (not discarded), preserving order x,y,z.
	got := MergeDocuments(
		[]Document{{ID: "x"}, {ID: "y"}},
		[]Document{
			{ID: "y", Images: []Image{{SourceID: "y", MediaType: "image/png", Data: []byte{1}}}},
			{ID: "z"},
		},
	)
	assertIDs(t, got, "x", "y", "z")
	if len(got[1].Images) != 1 {
		t.Errorf("expected media merged onto existing 'y', got %d images", len(got[1].Images))
	}
}

func TestMergeDocuments_NoDoubleAttachSameSourceID(t *testing.T) {
	img := Image{SourceID: "y", MediaType: "image/png", Data: []byte{1}}
	got := MergeDocuments([]Document{{ID: "y", Images: []Image{img}}}, []Document{{ID: "y", Images: []Image{img}}})
	if len(got) != 1 || len(got[0].Images) != 1 {
		t.Fatalf("expected single doc with a single image, got %d docs / %d images", len(got), len(got[0].Images))
	}

	// A distinct SourceID DOES attach.
	got = MergeDocuments(got, []Document{{ID: "y", Images: []Image{{SourceID: "y-att", MediaType: "image/png", Data: []byte{2}}}}})
	if len(got[0].Images) != 2 {
		t.Errorf("distinct SourceID should attach; got %d images", len(got[0].Images))
	}
}

func TestMergeDocuments_MergesPDFs(t *testing.T) {
	got := MergeDocuments(
		[]Document{{ID: "doc"}},
		[]Document{{ID: "doc", PDFs: []PDF{{SourceID: "doc", MediaType: "application/pdf", Data: []byte("%PDF")}}}},
	)
	if len(got) != 1 || len(got[0].PDFs) != 1 {
		t.Errorf("expected PDF merged onto existing doc, got %d docs / %d pdfs", len(got), len(got[0].PDFs))
	}
}

// The initial documents are re-sent unchanged on every round of a turn, so
// merging a tool call's media into the cumulative list must not leak into
// them through a shared backing array.
func TestAllDocuments_LeavesInitialDocumentsUntouched(t *testing.T) {
	images := make([]Image, 1, 4) // spare capacity: an in-place append would be visible
	images[0] = Image{SourceID: "a-1"}
	req := GenerateRequest{
		Documents: []Document{{ID: "a", Images: images}},
		Messages: []Message{
			{Role: RoleUser, Content: "q"},
			{Role: RoleTool, Documents: []Document{{ID: "a", Images: []Image{{SourceID: "a-2"}}}}},
		},
	}

	all := AllDocuments(req)

	if len(all[0].Images) != 2 {
		t.Fatalf("cumulative doc should carry both images, got %d", len(all[0].Images))
	}
	if len(req.Documents[0].Images) != 1 || images[:2][1].SourceID != "" {
		t.Errorf("initial document was modified: %+v", req.Documents[0].Images[:cap(req.Documents[0].Images)])
	}
}

func TestAllDocuments_NumbersToolDocumentsAfterInitialOnes(t *testing.T) {
	req := GenerateRequest{
		Documents: []Document{{ID: "a"}, {ID: "b"}},
		Messages: []Message{
			{Role: RoleUser, Content: "q"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t1"}}},
			{Role: RoleTool, ToolCallID: "t1", Documents: []Document{{ID: "c"}, {ID: "a"}}},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t2"}}},
			{Role: RoleTool, ToolCallID: "t2", Documents: []Document{{ID: "d"}, {ID: "c"}}},
			// Documents on a non-tool turn are not evidence.
			{Role: RoleUser, Content: "follow-up", Documents: []Document{{ID: "ignored"}}},
		},
	}
	assertIDs(t, AllDocuments(req), "a", "b", "c", "d")
}
