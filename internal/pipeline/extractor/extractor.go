// Package extractor provides content extraction from various file formats into plain text.
package extractor

import "context"

type Extractor interface {
	CanExtract(contentType string) bool
	Extract(ctx context.Context, raw []byte) (string, error)
}
