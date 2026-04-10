// Package chunking splits text into overlapping chunks for embedding.
package chunking

import (
	"strings"
)

// DefaultMaxTokens is the target chunk size in approximate tokens (words).
const DefaultMaxTokens = 500

// DefaultOverlapTokens is the number of overlapping tokens between chunks.
const DefaultOverlapTokens = 100

// MaxChunkBytes is a hard upper bound on a single chunk's size, in bytes.
// Even when text contains very few whitespace boundaries (long base64 blobs,
// minified HTML, quoted-printable bodies), no chunk should exceed this limit
// — embedding APIs reject oversized inputs (Voyage caps at ~32k tokens, OpenAI
// at 8192 tokens). Roughly 8000 bytes ≈ 2000 ASCII tokens, well under any
// provider's per-input limit.
const MaxChunkBytes = 8000

// Chunk represents a segment of a document.
type Chunk struct {
	Index int
	Text  string
}

// Split divides text into overlapping chunks of approximately maxTokens words.
// If the text is shorter than maxTokens AND fits under MaxChunkBytes, a single
// chunk is returned. Otherwise the text is split by word boundaries with the
// configured overlap, and any chunk that still exceeds MaxChunkBytes is
// further sliced into byte-bounded pieces — this is the safety net for
// pathological inputs like base64 blobs that have very few whitespace breaks.
func Split(text string, maxTokens, overlapTokens int) []Chunk {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	if overlapTokens <= 0 {
		overlapTokens = DefaultOverlapTokens
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	words := strings.Fields(text)
	if len(words) <= maxTokens && len(text) <= MaxChunkBytes {
		return []Chunk{{Index: 0, Text: text}}
	}

	var chunks []Chunk
	index := 0
	emit := func(s string) {
		// If a single word-bounded chunk is still too large (e.g. one long
		// base64 blob with no whitespace), split it into byte-bounded pieces.
		for len(s) > MaxChunkBytes {
			chunks = append(chunks, Chunk{Index: index, Text: s[:MaxChunkBytes]})
			index++
			s = s[MaxChunkBytes:]
		}
		if s != "" {
			chunks = append(chunks, Chunk{Index: index, Text: s})
			index++
		}
	}

	start := 0
	for start < len(words) {
		end := start + maxTokens
		if end > len(words) {
			end = len(words)
		}
		emit(strings.Join(words[start:end], " "))

		// Move forward by (maxTokens - overlap)
		step := maxTokens - overlapTokens
		if step <= 0 {
			step = 1
		}
		start += step
	}

	return chunks
}
