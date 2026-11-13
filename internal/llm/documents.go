package llm

// MergeDocuments extends dst with the src entries whose ID isn't already
// present, preserving dst's order. Citation indices for [N]-marker providers
// are positions in this merged list, so the order must stay append-only.
//
// A src entry whose ID is already present contributes only its media: the
// nexus_open_attachment tool returns a chunk that is always already in dst as
// text, and dropping the media-bearing copy would tell the model "image
// attached below" for media it never received.
func MergeDocuments(dst, src []Document) []Document {
	if len(src) == 0 {
		return dst
	}
	idx := make(map[string]int, len(dst)+len(src))
	for i, d := range dst {
		idx[d.ID] = i
	}
	for _, d := range src {
		if i, ok := idx[d.ID]; ok {
			dst[i].Images = mergeBySourceID(dst[i].Images, d.Images, func(im Image) string { return im.SourceID })
			dst[i].PDFs = mergeBySourceID(dst[i].PDFs, d.PDFs, func(p PDF) string { return p.SourceID })
			continue
		}
		idx[d.ID] = len(dst)
		dst = append(dst, d)
	}
	return dst
}

// mergeBySourceID appends the src items whose key isn't already in dst. An
// empty key always appends, so distinct keyless payloads aren't collapsed.
func mergeBySourceID[T any](dst, src []T, key func(T) string) []T {
	if len(src) == 0 {
		return dst
	}
	have := make(map[string]struct{}, len(dst))
	for _, it := range dst {
		if k := key(it); k != "" {
			have[k] = struct{}{}
		}
	}
	// Copy-on-grow: dst may share its backing array with the caller's
	// original document, which must not see the appended items.
	out := make([]T, len(dst), len(dst)+len(src))
	copy(out, dst)
	for _, it := range src {
		if k := key(it); k != "" {
			if _, ok := have[k]; ok {
				continue
			}
			have[k] = struct{}{}
		}
		out = append(out, it)
	}
	return out
}

// AllDocuments returns every document the request carries, in citation order:
// the initial Documents, then each tool turn's Documents in message order,
// merged with MergeDocuments. Adapters that render documents as one numbered
// block (OpenAI, Ollama) use it; the caller's citation parser must number
// documents the same way.
func AllDocuments(req GenerateRequest) []Document {
	all := append([]Document(nil), req.Documents...)
	for _, m := range req.Messages {
		if m.Role == RoleTool {
			all = MergeDocuments(all, m.Documents)
		}
	}
	return all
}
