// Package chunking splits text into overlapping chunks for embedding.
package chunking

import (
	"strings"
)

// DefaultMaxTokens is the target chunk size in approximate tokens (words).
const DefaultMaxTokens = 500

// DefaultOverlapTokens is the number of overlapping tokens between chunks.
const DefaultOverlapTokens = 100

// Chunk represents a segment of a document.
type Chunk struct {
	Index int
	Text  string
}

// Split divides text into overlapping chunks of approximately maxTokens words.
// If the text is shorter than maxTokens, a single chunk is returned.
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
	if len(words) <= maxTokens {
		return []Chunk{{Index: 0, Text: text}}
	}

	var chunks []Chunk
	start := 0
	index := 0

	for start < len(words) {
		end := start + maxTokens
		if end > len(words) {
			end = len(words)
		}

		chunkText := strings.Join(words[start:end], " ")
		chunks = append(chunks, Chunk{Index: index, Text: chunkText})
		index++

		// Move forward by (maxTokens - overlap)
		step := maxTokens - overlapTokens
		if step <= 0 {
			step = 1
		}
		start += step
	}

	return chunks
}
