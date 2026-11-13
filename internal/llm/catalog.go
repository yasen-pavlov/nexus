package llm

// Curated model catalog. Capability flags drive both UI affordances (vision
// chip, tool chip, citations chip) and orchestrator decisions (only attach
// images when SupportsVision; only request native citations when
// SupportsCitations).
//
// Last verified: 2026-11-13, against the providers' model, pricing and
// deprecation pages. Refresh when providers ship new top-tier models; admins
// can also add custom IDs from the settings UI without editing this list (the
// registry treats unknown IDs as "tools/vision unknown" with safe defaults).
//
// TypicalTTFTms is time to first visible token on a grounded answer. For
// models that reason first it sits between a simple lookup (no reasoning,
// well under a second) and a multi-step question (2-4s of reasoning).
// Anthropic values were measured against the live API; the GPT-6 values are
// unmeasured and left at zero, which hides the caption in the UI.

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderOllama    = "ollama"
)

// Catalog is the seed list. Concrete *ModelInfo values; the Provider and
// BareID fields are populated by ProviderModels for convenience.
var catalog = []ModelInfo{
	// Anthropic — native citations, prompt caching, vision, tools. The 5.x
	// models reason by default; the adapter runs them at medium effort.
	{
		ID:                "anthropic:claude-fable-5-2",
		Provider:          ProviderAnthropic,
		BareID:            "claude-fable-5-2",
		DisplayName:       "Claude Fable 5.2 (deepest reasoning)",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  8.0,
		OutputCostPerMtok: 40.0,
		TypicalTTFTms:     2500,
	},
	{
		ID:                "anthropic:claude-opus-5-5",
		Provider:          ProviderAnthropic,
		BareID:            "claude-opus-5-5",
		DisplayName:       "Claude Opus 5.5",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  4.0,
		OutputCostPerMtok: 20.0,
		// Always reasons before answering, even on simple lookups.
		TypicalTTFTms: 3500,
	},
	{
		ID:                "anthropic:claude-sonnet-5-5",
		Provider:          ProviderAnthropic,
		BareID:            "claude-sonnet-5-5",
		DisplayName:       "Claude Sonnet 5.5 (recommended)",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  2.0,
		OutputCostPerMtok: 10.0,
		TypicalTTFTms:     1500,
	},
	{
		ID:                "anthropic:claude-haiku-5-5",
		Provider:          ProviderAnthropic,
		BareID:            "claude-haiku-5-5",
		DisplayName:       "Claude Haiku 5.5 (fast / cheap rewriter)",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		// Prices for prompts up to 100k tokens; above that Anthropic bills
		// $0.50 / $2.50. Grounded answers and rewrites stay far below it.
		InputCostPerMtok:  0.10,
		OutputCostPerMtok: 0.50,
		// As an answer model it reasons more readily than Sonnet 5.5. As the
		// rewriter it runs with reasoning off and answers in under a second.
		TypicalTTFTms: 2000,
	},
	// Previous generation, still served by Anthropic (earliest retirement
	// February 2027). Kept so existing chats and stored defaults keep working.
	{
		ID:                "anthropic:claude-opus-4-7",
		Provider:          ProviderAnthropic,
		BareID:            "claude-opus-4-7",
		DisplayName:       "Claude Opus 4.7",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  5.0,
		OutputCostPerMtok: 25.0,
		TypicalTTFTms:     500,
	},
	{
		ID:                "anthropic:claude-sonnet-4-6",
		Provider:          ProviderAnthropic,
		BareID:            "claude-sonnet-4-6",
		DisplayName:       "Claude Sonnet 4.6",
		ContextWindow:     1_000_000,
		SupportsCitations: true,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
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
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  1.0,
		OutputCostPerMtok: 5.0,
		TypicalTTFTms:     400,
	},

	// OpenAI — no native citations (orchestrator [N] parser fills in),
	// automatic prefix caching, vision, tools. All reasoning models, served
	// through the Responses API. Prompts above 272k input tokens are billed
	// at 2x input / 1.5x output; the prices here are the standard tier.
	{
		ID:                "openai:gpt-6-astra",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-6-astra",
		DisplayName:       "GPT-6 Astra (flagship)",
		ContextWindow:     1_050_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  10.0,
		OutputCostPerMtok: 50.0,
	},
	{
		ID:                "openai:gpt-6.1-sol",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-6.1-sol",
		DisplayName:       "GPT-6.1 Sol (recommended)",
		ContextWindow:     1_050_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  2.0,
		OutputCostPerMtok: 10.0,
	},
	{
		ID:                "openai:gpt-6.1-terra",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-6.1-terra",
		DisplayName:       "GPT-6.1 Terra (fast)",
		ContextWindow:     1_050_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  1.0,
		OutputCostPerMtok: 5.0,
	},
	{
		ID:                "openai:gpt-6-luna",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-6-luna",
		DisplayName:       "GPT-6 Luna (fast / cheap rewriter)",
		ContextWindow:     1_050_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		// Accepts reasoning effort "none", which is what makes it usable
		// inside the rewriter's 3s budget.
		InputCostPerMtok:  0.10,
		OutputCostPerMtok: 0.50,
	},
	// Retiring: OpenAI shuts these down on the dates shown. Kept until then
	// so existing chats and stored defaults keep working; remove afterwards.
	{
		ID:                "openai:gpt-5",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-5",
		DisplayName:       "GPT-5 (retires Dec 11, 2026)",
		ContextWindow:     400_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  1.25,
		OutputCostPerMtok: 10.0,
		// Medium-effort reasoning before any visible token: real-world TTFT
		// on long-context grounded answers (10+ docs) ran 15-30s.
		TypicalTTFTms: 18000,
	},
	{
		ID:                "openai:gpt-4.1",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-4.1",
		DisplayName:       "GPT-4.1 (retires Jan 14, 2027)",
		ContextWindow:     1_000_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  2.0,
		OutputCostPerMtok: 8.0,
		TypicalTTFTms:     600,
	},
	{
		ID:                "openai:gpt-5-mini",
		Provider:          ProviderOpenAI,
		BareID:            "gpt-5-mini",
		DisplayName:       "GPT-5 mini (retires Dec 11, 2026)",
		ContextWindow:     400_000,
		SupportsCitations: false,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsPDF:       true,
		SupportsCaching:   true,
		InputCostPerMtok:  0.25,
		OutputCostPerMtok: 2.0,
		// Reasoning-family model: a few seconds of thinking before any
		// visible output, too slow for the rewriter's 3s budget.
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

// recommendedDefaults is the answer model per provider, in provider
// preference order, for an install whose admin hasn't chosen a default.
var recommendedDefaults = []string{
	"anthropic:claude-sonnet-5-5",
	"openai:gpt-6.1-sol",
	"ollama:qwen3:14b",
}

// RecommendedDefault picks the default answer model from the models a user
// can see: the first recommended model that is visible, else the first
// visible model, else "". Without it the fallback is whatever sorts first by
// display name, which is the most expensive Claude model.
func RecommendedDefault(visible []ModelInfo) string {
	for _, id := range recommendedDefaults {
		for _, m := range visible {
			if m.ID == id {
				return id
			}
		}
	}
	if len(visible) > 0 {
		return visible[0].ID
	}
	return ""
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
