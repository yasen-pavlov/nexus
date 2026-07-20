package chunking

import (
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSplit_ByteBoundedSingleWord(t *testing.T) {
	// A single very long "word" with no whitespace — like a base64 attachment.
	// Word count is 1 (well under maxTokens) but byte count is huge.
	bigWord := strings.Repeat("A", MaxChunkBytes*3+50)
	chunks := Split(bigWord, 500, 100)

	if len(chunks) < 4 {
		t.Errorf("expected at least 4 byte-bounded chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c.Text) > MaxChunkBytes {
			t.Errorf("chunk %d exceeds MaxChunkBytes: %d > %d", i, len(c.Text), MaxChunkBytes)
		}
		if c.Index != i {
			t.Errorf("chunk %d has Index=%d", i, c.Index)
		}
	}
}

func TestSplit_ByteBoundedMixedContent(t *testing.T) {
	// Realistic email-like content: some normal text followed by a giant base64 blob
	prose := strings.Repeat("Hello world this is a normal email body. ", 50)
	blob := strings.Repeat("a", MaxChunkBytes*2)
	text := prose + blob

	chunks := Split(text, 500, 100)
	for i, c := range chunks {
		if len(c.Text) > MaxChunkBytes {
			t.Errorf("chunk %d exceeds MaxChunkBytes: %d > %d", i, len(c.Text), MaxChunkBytes)
		}
	}
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks for blob input, got %d", len(chunks))
	}
}

func TestSplit_ShortTextOverByteLimit(t *testing.T) {
	// Word count well under maxTokens but a single word over MaxChunkBytes
	bigWord := strings.Repeat("x", MaxChunkBytes*2)
	chunks := Split(bigWord, 500, 100)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks (byte-bounded), got %d", len(chunks))
	}
}

// TestSplit_ByteBoundedMultibyteRuneSafe verifies the pathological-input
// byte slicer never splits a multibyte UTF-8 rune. "д" is 2 bytes, so a
// naive cut at an even byte cap (8000) lands mid-rune; the rune-boundary
// backoff must trim to a valid boundary instead.
func TestSplit_ByteBoundedMultibyteRuneSafe(t *testing.T) {
	// Cyrillic blob with no whitespace, well over the byte cap.
	blob := strings.Repeat("д", MaxChunkBytes*2) // each "д" is 2 bytes UTF-8
	chunks := Split(blob, 500, 100)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 byte-bounded chunks, got %d", len(chunks))
	}
	var reassembled strings.Builder
	for i, c := range chunks {
		if len(c.Text) > MaxChunkBytes {
			t.Errorf("chunk %d exceeds MaxChunkBytes: %d > %d", i, len(c.Text), MaxChunkBytes)
		}
		if !utf8.ValidString(c.Text) {
			t.Errorf("chunk %d is not valid UTF-8 — a multibyte rune was split", i)
		}
		reassembled.WriteString(c.Text)
	}
	// No bytes are lost or duplicated across the rune-boundary backoff.
	if reassembled.String() != blob {
		t.Errorf("reassembled chunks differ from input (len %d vs %d)", reassembled.Len(), len(blob))
	}
}

// TestSplit_ByteBoundedMultibyteOddOffset uses a 3-byte rune to ensure the
// backoff handles cut points that are off by 1 and 2 bytes from a boundary.
func TestSplit_ByteBoundedMultibyteOddOffset(t *testing.T) {
	blob := strings.Repeat("€", MaxChunkBytes) // each "€" is 3 bytes UTF-8
	chunks := Split(blob, 500, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if !utf8.ValidString(c.Text) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
	}
}

// TestSplit_InvalidUTF8DoesNotHang covers the infinite-loop/OOM fix: a long run
// of UTF-8 continuation bytes with no whitespace. Pre-fix this hung Split (and
// the async flush goroutine) forever; post-fix the sanitizer replaces the
// invalid bytes and Split returns promptly with valid, bounded chunks.
func TestSplit_InvalidUTF8DoesNotHang(t *testing.T) {
	blob := strings.Repeat("\x80", MaxChunkBytes*2+50)
	chunks := Split(blob, 500, 100)
	for _, c := range chunks {
		if !utf8.ValidString(c.Text) {
			t.Errorf("chunk is not valid UTF-8 after sanitize: %q", c.Text)
		}
		if len(c.Text) > MaxChunkBytes {
			t.Errorf("chunk exceeds MaxChunkBytes: %d", len(c.Text))
		}
	}
}

// TestAppendByteBounded_InvalidUTF8Terminates directly exercises the
// byte-bounded splitter's cut==0 guard with raw continuation bytes (bypassing
// Split's sanitizer). It must terminate, cut every piece to (0, MaxChunkBytes],
// and neither lose nor duplicate bytes.
func TestAppendByteBounded_InvalidUTF8Terminates(t *testing.T) {
	s := strings.Repeat("\x80", MaxChunkBytes*3+7)
	idx := 0
	chunks := appendByteBounded(nil, s, &idx)
	total := 0
	for _, c := range chunks {
		if len(c.Text) == 0 || len(c.Text) > MaxChunkBytes {
			t.Errorf("piece length %d out of (0, MaxChunkBytes]", len(c.Text))
		}
		total += len(c.Text)
	}
	if total != len(s) {
		t.Errorf("byte total = %d, want %d (no bytes lost or duplicated)", total, len(s))
	}
}
