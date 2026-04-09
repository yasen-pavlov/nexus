package search

import (
	"testing"

	"github.com/muty/nexus/internal/model"
)

func TestApplyMetadataBonus_EmailSenderMatch(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Random email", SourceType: "imap",
				Metadata: map[string]any{"from": "Bob <bob@example.com>"}}, Rank: 0.5},
			{Document: model.Document{Title: "Another email", SourceType: "imap",
				Metadata: map[string]any{"from": "Alice <alice@example.com>"}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "Alice")

	if result.Documents[0].Metadata["from"] != "Alice <alice@example.com>" {
		t.Errorf("expected Alice's email first, got %v", result.Documents[0].Metadata["from"])
	}
	if result.Documents[0].Rank <= result.Documents[1].Rank {
		t.Error("Alice email should have higher score after bonus")
	}
}

func TestApplyMetadataBonus_PaperlessCorrespondent(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Invoice", SourceType: "paperless",
				Metadata: map[string]any{"correspondent": "IKEA"}}, Rank: 0.5},
			{Document: model.Document{Title: "Receipt", SourceType: "paperless",
				Metadata: map[string]any{"correspondent": "Amazon"}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "ikea")

	if result.Documents[0].Metadata["correspondent"] != "IKEA" {
		t.Errorf("expected IKEA doc first, got %v", result.Documents[0].Metadata["correspondent"])
	}
}

func TestApplyMetadataBonus_PaperlessTags(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Doc A", SourceType: "paperless",
				Metadata: map[string]any{"tags": []any{"finance", "tax"}}}, Rank: 0.5},
			{Document: model.Document{Title: "Doc B", SourceType: "paperless",
				Metadata: map[string]any{"tags": []any{"medical"}}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "tax document")

	if result.Documents[0].Title != "Doc A" {
		t.Errorf("expected Doc A (tagged tax) first, got %q", result.Documents[0].Title)
	}
}

func TestApplyMetadataBonus_FilesystemPath(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "notes.md", SourceType: "filesystem",
				Metadata: map[string]any{"path": "projects/budget/notes.md"}}, Rank: 0.5},
			{Document: model.Document{Title: "readme.md", SourceType: "filesystem",
				Metadata: map[string]any{"path": "docs/readme.md"}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "budget")

	if result.Documents[0].Title != "notes.md" {
		t.Errorf("expected budget doc first, got %q", result.Documents[0].Title)
	}
}

func TestApplyMetadataBonus_AttachmentFilenames(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Email with attachment", SourceType: "imap",
				Metadata: map[string]any{"attachment_filenames": []any{"invoice.pdf", "receipt.pdf"}}}, Rank: 0.5},
			{Document: model.Document{Title: "Plain email", SourceType: "imap",
				Metadata: map[string]any{}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "invoice")

	if result.Documents[0].Title != "Email with attachment" {
		t.Errorf("expected email with invoice attachment first, got %q", result.Documents[0].Title)
	}
}

func TestApplyMetadataBonus_NoMatch(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Doc", SourceType: "imap",
				Metadata: map[string]any{"from": "Bob"}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "Alice")

	if result.Documents[0].Rank != 0.5 {
		t.Errorf("score changed without match: %f, want 0.5", result.Documents[0].Rank)
	}
}

func TestApplyMetadataBonus_EmptyQuery(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Doc", SourceType: "imap"}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "")

	if result.Documents[0].Rank != 0.5 {
		t.Errorf("score changed with empty query: %f", result.Documents[0].Rank)
	}
}

func TestApplyMetadataBonus_EmptyResults(t *testing.T) {
	result := &model.SearchResult{}
	ApplyMetadataBonus(result, "test") // should not panic
}

func TestApplyMetadataBonus_TelegramChatName(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Chat from Alice", SourceType: "telegram",
				Metadata: map[string]any{"chat_name": "Alice"}}, Rank: 0.5},
			{Document: model.Document{Title: "Chat from Bob", SourceType: "telegram",
				Metadata: map[string]any{"chat_name": "Bob"}}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "Alice")

	if result.Documents[0].Metadata["chat_name"] != "Alice" {
		t.Errorf("expected Alice's chat first, got %v", result.Documents[0].Metadata["chat_name"])
	}
	if result.Documents[0].Rank <= result.Documents[1].Rank {
		t.Error("Alice chat should have higher score after bonus")
	}
}

func TestApplyMetadataBonus_NilMetadata(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Doc", SourceType: "imap"}, Rank: 0.5},
		},
	}

	ApplyMetadataBonus(result, "test")

	if result.Documents[0].Rank != 0.5 {
		t.Errorf("score changed with nil metadata: %f", result.Documents[0].Rank)
	}
}

func TestApplyMetadataBonus_PartialTermMatch(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Email", SourceType: "imap",
				Metadata: map[string]any{"from": "Alice Smith"}}, Rank: 0.5},
		},
	}

	// Query has 2 terms, only "alice" matches metadata
	ApplyMetadataBonus(result, "alice invoice")

	// Should get partial bonus (1/2 terms matched)
	expectedBonus := metadataBonus * 0.5
	if result.Documents[0].Rank < 0.5+expectedBonus*0.9 || result.Documents[0].Rank > 0.5+expectedBonus*1.1 {
		t.Errorf("expected partial bonus ~%f, got rank %f", 0.5+expectedBonus, result.Documents[0].Rank)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello world", 2},
		{"Alice", 1},
		{"a b c", 0},      // single chars filtered
		{"  spaces  ", 1}, // "spaces" only
		{"", 0},
		{"hello-world", 1}, // kept as one term
	}
	for _, tt := range tests {
		got := tokenize(tt.input)
		if len(got) != tt.want {
			t.Errorf("tokenize(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}
