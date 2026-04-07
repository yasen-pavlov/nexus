package chunking

import (
	"strings"
	"testing"
)

func TestSplit_EmptyString(t *testing.T) {
	chunks := Split("", 500, 100)
	if chunks != nil {
		t.Errorf("expected nil for empty string, got %d chunks", len(chunks))
	}
}

func TestSplit_WhitespaceOnly(t *testing.T) {
	chunks := Split("   \n\n  ", 500, 100)
	if chunks != nil {
		t.Errorf("expected nil for whitespace, got %d chunks", len(chunks))
	}
}

func TestSplit_ShortText(t *testing.T) {
	text := "This is a short document that should not be split."
	chunks := Split(text, 500, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Index != 0 {
		t.Errorf("expected index 0, got %d", chunks[0].Index)
	}
	if chunks[0].Text != text {
		t.Errorf("expected original text, got %q", chunks[0].Text)
	}
}

func TestSplit_ExactlyMaxTokens(t *testing.T) {
	words := make([]string, 500)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	chunks := Split(text, 500, 100)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for exactly maxTokens, got %d", len(chunks))
	}
}

func TestSplit_LongText(t *testing.T) {
	// Create 1000-word text
	words := make([]string, 1000)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	chunks := Split(text, 500, 100)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Verify indices are sequential
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d has index %d", i, c.Index)
		}
	}

	// Verify each chunk has content
	for i, c := range chunks {
		if c.Text == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}

	// Verify first chunk has ~500 words
	firstWords := len(strings.Fields(chunks[0].Text))
	if firstWords != 500 {
		t.Errorf("expected first chunk ~500 words, got %d", firstWords)
	}
}

func TestSplit_OverlapPresent(t *testing.T) {
	// Create 600-word text with numbered words for verification
	words := make([]string, 600)
	for i := range words {
		words[i] = strings.Repeat("x", i%10+1) // varied words
	}
	text := strings.Join(words, " ")

	chunks := Split(text, 500, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Check that the end of chunk 0 overlaps with the start of chunk 1
	words0 := strings.Fields(chunks[0].Text)
	words1 := strings.Fields(chunks[1].Text)

	// Last 100 words of chunk 0 should appear at start of chunk 1
	overlap0 := words0[len(words0)-100:]
	overlap1 := words1[:100]

	for i := range overlap0 {
		if overlap0[i] != overlap1[i] {
			t.Errorf("overlap mismatch at position %d: %q != %q", i, overlap0[i], overlap1[i])
			break
		}
	}
}

func TestSplit_DefaultValues(t *testing.T) {
	words := make([]string, 1000)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	// Zero values should use defaults
	chunks := Split(text, 0, 0)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks with defaults, got %d", len(chunks))
	}
}

func TestSplit_VeryLargeOverlap(t *testing.T) {
	words := make([]string, 100)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	// Overlap > maxTokens should still work (step becomes 1)
	chunks := Split(text, 10, 20)
	if len(chunks) == 0 {
		t.Error("expected at least 1 chunk")
	}
}
