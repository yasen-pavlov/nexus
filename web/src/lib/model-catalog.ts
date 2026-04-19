// Curated model catalog per provider. The admin UI renders these as a
// combobox — predefined options accelerate the happy path, but a custom
// string is always accepted (providers release new models faster than we
// can ship a release), and passing `value` through to the backend verbatim
// lets us skip a server-side allow-list.

export interface ModelOption {
  value: string;
  label: string;
  dimension?: number;
  notes?: string;
}

export type EmbeddingProvider = "" | "ollama" | "openai" | "voyage" | "cohere";
export type RerankProvider = "" | "voyage" | "cohere";

export const EMBEDDING_PROVIDERS: { value: EmbeddingProvider; label: string; hint?: string }[] = [
  { value: "", label: "Disabled", hint: "BM25-only search" },
  { value: "ollama", label: "Ollama", hint: "Self-hosted" },
  { value: "openai", label: "OpenAI" },
  { value: "voyage", label: "Voyage AI" },
  { value: "cohere", label: "Cohere" },
];

export const RERANK_PROVIDERS: { value: RerankProvider; label: string }[] = [
  { value: "", label: "Disabled" },
  { value: "voyage", label: "Voyage AI" },
  { value: "cohere", label: "Cohere" },
];

export const EMBEDDING_MODELS: Record<EmbeddingProvider, ModelOption[]> = {
  "": [],
  ollama: [
    { value: "nomic-embed-text", label: "nomic-embed-text", dimension: 768, notes: "Good balance, 768-d" },
    { value: "mxbai-embed-large", label: "mxbai-embed-large", dimension: 1024, notes: "Higher quality" },
    { value: "snowflake-arctic-embed", label: "snowflake-arctic-embed", notes: "Retrieval-tuned" },
    { value: "all-minilm", label: "all-minilm", dimension: 384, notes: "Fast + small" },
  ],
  openai: [
    { value: "text-embedding-3-small", label: "text-embedding-3-small", dimension: 1536, notes: "Cheap default" },
    { value: "text-embedding-3-large", label: "text-embedding-3-large", dimension: 3072, notes: "Highest quality" },
  ],
  voyage: [
    { value: "voyage-4-large", label: "voyage-4-large", dimension: 1024, notes: "Recommended" },
    { value: "voyage-3-large", label: "voyage-3-large", dimension: 1024, notes: "Previous flagship" },
    { value: "voyage-3", label: "voyage-3", dimension: 1024, notes: "v3 base" },
    { value: "voyage-3-lite", label: "voyage-3-lite", dimension: 512, notes: "Lower cost" },
    { value: "voyage-code-3", label: "voyage-code-3", dimension: 1024, notes: "Code-aware" },
    { value: "voyage-multilingual-2", label: "voyage-multilingual-2", dimension: 1024, notes: "Multilingual" },
    { value: "voyage-law-2", label: "voyage-law-2", dimension: 1024, notes: "Legal / financial" },
  ],
  cohere: [
    { value: "embed-v4.0", label: "embed-v4.0", dimension: 1024, notes: "Latest" },
    { value: "embed-multilingual-v3.0", label: "embed-multilingual-v3.0", dimension: 1024, notes: "Multilingual" },
    { value: "embed-english-v3.0", label: "embed-english-v3.0", dimension: 1024, notes: "English-only" },
  ],
};

export const RERANK_MODELS: Record<RerankProvider, ModelOption[]> = {
  "": [],
  voyage: [
    { value: "rerank-2", label: "rerank-2", notes: "Recommended" },
    { value: "rerank-2-lite", label: "rerank-2-lite", notes: "Lower cost" },
    { value: "rerank-1", label: "rerank-1", notes: "Legacy" },
  ],
  cohere: [
    { value: "rerank-v3.5", label: "rerank-v3.5", notes: "Latest" },
    { value: "rerank-multilingual-v3.0", label: "rerank-multilingual-v3.0", notes: "Multilingual" },
    { value: "rerank-english-v3.0", label: "rerank-english-v3.0", notes: "English-only" },
  ],
};

// Default-model convenience: the combobox pre-fills this when the provider
// changes. Matches the BE's inferred defaults so picking a provider + saving
// "just works" without having to manually type a model name.
export const DEFAULT_EMBEDDING_MODEL: Record<EmbeddingProvider, string> = {
  "": "",
  ollama: "nomic-embed-text",
  openai: "text-embedding-3-small",
  voyage: "voyage-4-large",
  cohere: "embed-v4.0",
};

export const DEFAULT_RERANK_MODEL: Record<RerankProvider, string> = {
  "": "",
  voyage: "rerank-2",
  cohere: "rerank-v3.5",
};
