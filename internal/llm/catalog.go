package llm

// Curated model catalog. Capability flags drive both UI affordances (vision
// chip, tool chip, citations chip) and orchestrator decisions (only attach
// images when SupportsVision; only request native citations when
// SupportsCitations).
//
// Last verified: 2026-04-25. Refresh when providers ship new top-tier models;
// admins can also add custom IDs from the settings UI without editing this
// list (the registry treats unknown IDs as "tools/vision unknown" with safe
// defaults).

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderOllama    = "ollama"
)

// Catalog is the seed list. Concrete *ModelInfo values; the Provider and
// BareID fields are populated by ProviderModels for convenience.
var catalog = []ModelInfo{
	// Anthropic — native citations, prompt caching, vision, tools.
	{
		ID:                "anthropic:claude-opus-4-7",
		Provider:          ProviderAnthropic,
		BareID:            "claude-opus-4-7",
		DisplayName:       "Claude Opus 4.7",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsCaching:   true,
		InputCostPerMtok:  5.0,
		OutputCostPerMtok: 25.0,
		TypicalTTFTms:     500,
	},
	{
		ID:                "anthropic:claude-sonnet-4-6",
		Provider:          ProviderAnthropic,
		BareID:            "claude-sonnet-4-6",
		DisplayName:       "Claude Sonnet 4.6 (recommended)",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsCaching:   true,
		InputCostPerMtok:  3.0,
		OutputCostPerMtok: 15.0,
		TypicalTTFTms:     700,
	},
	{
		ID:                "anthropic:claude-haiku-4-5",
		Provider:          ProviderAnthropic,
		BareID:            "claude-haiku-4-5",
		DisplayName:       "Claude Haiku 4.5",
		ContextWindow:     200_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsCaching:   true,
		InputCostPerMtok:  1.0,
		OutputCostPerMtok: 5.0,
		TypicalTTFTms:     400,
	},

	// OpenAI — no native citations (orchestrator [N] parser fills in),
	// automatic prefix caching, vision, tools.
	{
		ID:                "openai:gpt-5",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-5",
		DisplayName:       "GPT-5",
		ContextWindow:     400_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsCaching:   true,
		InputCostPerMtok:  1.25,
		OutputCostPerMtok: 10.0,
		// GPT-5 is a reasoning model — the OpenAI Chat Completions
		// API runs medium-effort reasoning before emitting tokens, so
		// real-world TTFT on long-context grounded answers (10+ docs)
		// runs 15-30s. Catalog value reflects observed median; admins
		// who need faster responses should pick gpt-4.1.
		TypicalTTFTms: 18000,
	},
	{
		ID:                "openai:gpt-4.1",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-4.1",
		DisplayName:       "GPT-4.1 (long-context value)",
		ContextWindow:     1_000_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsCaching:   true,
		InputCostPerMtok:  2.0,
		OutputCostPerMtok: 8.0,
		TypicalTTFTms:     600,
	},
	{
		ID:                "openai:gpt-5-mini",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-5-mini",
		DisplayName:       "GPT-5 mini",
		ContextWindow:     400_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsCaching:   true,
		InputCostPerMtok:  0.25,
		OutputCostPerMtok: 2.0,
		// Reasoning-family model — same caveat as gpt-5. Lighter
		// effort than the full model but still spends a few seconds
		// thinking before any visible output. NOT suitable as the
		// rewriter (3s budget); use gpt-4.1 or claude-haiku-4-5.
		TypicalTTFTms: 6000,
	},

	// Ollama — local models. Tools support varies; streaming+tools is
	// shaky for some (the adapter falls back to non-streaming when tools
	// are present).
	{
		ID:                "ollama:qwen3:14b",
		Provider:          ProviderOllama,
		BareID:            "qwen3:14b",
		DisplayName:       "Qwen3 14B (local, 16GB VRAM)",
		ContextWindow:     128_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    false,
		SupportsCaching:   false,
		InputCostPerMtok:  0,
		OutputCostPerMtok: 0,
		TypicalTTFTms:     300,
	},
	{
		ID:                "ollama:gemma3:12b",
		Provider:          ProviderOllama,
		BareID:            "gemma3:12b",
		DisplayName:       "Gemma 3 12B (local)",
		ContextWindow:     128_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    false,
		SupportsCaching:   false,
	},
	{
		ID:                "ollama:command-r:35b",
		Provider:          ProviderOllama,
		BareID:            "command-r:35b",
		DisplayName:       "Command R 35B (local, 24GB VRAM, citation-grounded)",
		ContextWindow:     128_000,
		SupportsCitations: false, // built-in but not via the unified Citation event
		SupportsTools:     true,
		SupportsVision:    false,
		SupportsCaching:   false,
	},
	{
		ID:                "ollama:llama3.2-vision:11b",
		Provider:          ProviderOllama,
		BareID:            "llama3.2-vision:11b",
		DisplayName:       "Llama 3.2 Vision 11B (local)",
		ContextWindow:     128_000,
		SupportsCitations: false,
		SupportsTools:     false,
		SupportsVision:    true,
		SupportsCaching:   false,
	},
}

// Catalog returns a copy of the full curated catalog.
func Catalog() []ModelInfo {
	out := make([]ModelInfo, len(catalog))
	copy(out, catalog)
	return out
}

// LookupModel returns the catalog entry for a provider-prefixed id.
// Returns (zero, false) when not found — callers may then construct a
// best-effort ModelInfo for an unknown id (admin custom models).
func LookupModel(id string) (ModelInfo, bool) {
	for _, m := range catalog {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// CatalogForProvider returns the curated entries for one provider.
func CatalogForProvider(provider string) []ModelInfo {
	var out []ModelInfo
	for _, m := range catalog {
		if m.Provider == provider {
			out = append(out, m)
		}
	}
	return out
}
