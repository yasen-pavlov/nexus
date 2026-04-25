package llm

import (
	"fmt"
	"strings"
)

// providerGenerators maps provider name → constructed Generator. Populated by
// NewRegistry from per-provider clients the caller supplies (only the
// providers with credentials get a non-nil entry).
type providerGenerators map[string]Generator

// staticRegistry is the default Registry implementation. It composes a fixed
// set of provider generators with an optional allowlist of model ids and
// surfaces the catalog filtered by both.
type staticRegistry struct {
	generators providerGenerators
	// allowlist is a set of provider-prefixed model ids the admin has
	// allowed users to pick. Empty allowlist = surface every catalog entry
	// for which the provider is configured (sensible default for a fresh
	// install).
	allowlist map[string]bool
	// extras are admin-added custom model ids outside the curated catalog
	// (e.g. a freshly-pulled Ollama model). Treated as best-effort: tools/
	// vision/caching default to false for unknown ids.
	extras map[string]ModelInfo
}

// RegistryConfig is the constructor input.
type RegistryConfig struct {
	// Generators is the per-provider constructed Generator (e.g.
	// "anthropic" → *anthropic.Client wrapped by RetryGenerator). Provider
	// keys without a Generator are treated as not configured.
	Generators map[string]Generator
	// Allowlist of provider-prefixed model ids visible to users. If nil or
	// empty, every catalog model whose provider is configured is exposed.
	Allowlist []string
	// Extras lets admins add custom model ids the curated catalog doesn't
	// include — e.g. a freshly-pulled Ollama model.
	Extras []ModelInfo
}

// NewRegistry builds a Registry from configured providers + an optional
// allowlist + admin extras. A model id is resolvable iff (a) its provider has
// a Generator and (b) it appears in the catalog or extras.
func NewRegistry(cfg RegistryConfig) Registry {
	allow := make(map[string]bool, len(cfg.Allowlist))
	for _, id := range cfg.Allowlist {
		allow[id] = true
	}
	extras := make(map[string]ModelInfo, len(cfg.Extras))
	for _, m := range cfg.Extras {
		extras[m.ID] = m
	}
	return &staticRegistry{
		generators: cfg.Generators,
		allowlist:  allow,
		extras:     extras,
	}
}

// Get resolves a provider-prefixed model id to (Generator, ModelInfo).
// Returns an error when the id is malformed, the provider isn't configured,
// or the model isn't in the catalog/extras + allowlist.
func (r *staticRegistry) Get(id string) (Generator, ModelInfo, error) {
	provider, _, err := splitID(id)
	if err != nil {
		return nil, ModelInfo{}, err
	}

	gen, ok := r.generators[provider]
	if !ok || gen == nil {
		return nil, ModelInfo{}, fmt.Errorf("llm: provider %q is not configured", provider)
	}

	info, ok := r.lookup(id)
	if !ok {
		return nil, ModelInfo{}, fmt.Errorf("llm: model %q is not in the catalog or admin extras", id)
	}

	if len(r.allowlist) > 0 && !r.allowlist[id] {
		return nil, ModelInfo{}, fmt.Errorf("llm: model %q is not in the admin allowlist", id)
	}

	return gen, info, nil
}

// Models returns the visible models (provider configured + in catalog/extras
// + allowlist passes). The slice is sorted by provider then DisplayName for
// stable UI rendering.
func (r *staticRegistry) Models() []ModelInfo {
	visible := make([]ModelInfo, 0)
	for _, m := range catalog {
		if r.isVisible(m.ID) {
			visible = append(visible, m)
		}
	}
	for _, m := range r.extras {
		// extras override catalog rows (admin may relabel) — only add if
		// the catalog didn't already contribute this id.
		if _, inCatalog := LookupModel(m.ID); inCatalog {
			continue
		}
		if r.isVisible(m.ID) {
			visible = append(visible, m)
		}
	}
	sortModels(visible)
	return visible
}

func (r *staticRegistry) isVisible(id string) bool {
	provider, _, err := splitID(id)
	if err != nil {
		return false
	}
	if gen, ok := r.generators[provider]; !ok || gen == nil {
		return false
	}
	if len(r.allowlist) > 0 && !r.allowlist[id] {
		return false
	}
	return true
}

func (r *staticRegistry) lookup(id string) (ModelInfo, bool) {
	if m, ok := r.extras[id]; ok {
		return m, true
	}
	return LookupModel(id)
}

// splitID parses "provider:bareID" into the two parts. The bare id may itself
// contain colons (Ollama tags use them, e.g. "qwen3:14b") — only the first
// colon is the separator.
func splitID(id string) (provider, bare string, err error) {
	idx := strings.IndexByte(id, ':')
	if idx <= 0 || idx == len(id)-1 {
		return "", "", fmt.Errorf("llm: model id %q must be in the form provider:model", id)
	}
	return id[:idx], id[idx+1:], nil
}

// sortModels sorts in place by provider then DisplayName.
func sortModels(ms []ModelInfo) {
	// Small-N — insertion sort keeps the dep tree clean and the order stable.
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && less(ms[j-1], ms[j]); j-- {
			ms[j-1], ms[j] = ms[j], ms[j-1]
		}
	}
}

func less(a, b ModelInfo) bool {
	// "less" returns true when a should come AFTER b (we swap when true).
	if a.Provider != b.Provider {
		return a.Provider > b.Provider
	}
	return a.DisplayName > b.DisplayName
}
