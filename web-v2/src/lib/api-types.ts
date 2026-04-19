// Curated view of the auto-generated schema. The generated types mark
// everything optional (swaggo/swag v1 doesn't emit `required`) and don't
// know about the `{data, error}` response wrapper, so we re-export key
// schemas with tighter types here. Regenerate via `npm run gen:types`
// whenever the Go backend changes its swagger annotations.
import type { components } from "./api-schema";

type Schemas = components["schemas"];

// Unwrap + require: strip the generated `?` that swagger 2.0 forces on every
// field. Apply to response shapes the backend always fills in.
type Req<T> = { [K in keyof T]-?: NonNullable<T[K]> };

// Auth

// The User type intentionally keeps created_at optional — Me() builds it
// from JWT claims which don't carry a timestamp, so the /auth/me route
// returns a zero-time value. Concrete uses that render the date (Users
// page, "Your account" card) read from /api/users list instead.
export type User = Omit<
  Req<Schemas["internal_api.userResponse"]>,
  "role" | "created_at"
> & {
  role: "admin" | "user";
  created_at?: string;
};

export type AuthResponse = Omit<Req<Schemas["internal_api.authResponse"]>, "user"> & {
  user: User;
};

export interface HealthResponse {
  status: string;
  setup_required?: boolean;
}

// Search / documents
//
// The schema's DocumentHit is missing fields that live on the embedded
// `model.Document` (relations, conversation_id, imap_message_id, hidden,
// source_id, etc.) because swag v1 doesn't traverse embedded struct tags
// cleanly. We restore them here. Regen should catch the rest; fix the
// backend annotations when swaggo v2 lands.

export interface Relation {
  type: "attachment_of" | "reply_to" | "member_of_thread" | "member_of_window" | string;
  target_source_id?: string;
  target_id?: string;
}

export interface Document {
  id: string;
  source_type: string;
  source_name: string;
  source_id: string;
  title: string;
  content: string;
  mime_type?: string;
  size?: number;
  metadata?: Record<string, unknown>;
  relations?: Relation[];
  conversation_id?: string;
  imap_message_id?: string;
  hidden?: boolean;
  url?: string;
  visibility: string;
  created_at: string;
  indexed_at: string;
}

export interface DocumentHit extends Document {
  rank: number;
  headline?: string;
  // Total relations (outgoing + incoming). Populated by the backend so the
  // UI can hide the "Related" toggle without fanning out /related per hit.
  related_count?: number;

  // Pinpoint-match fields — populated when the backend can map the
  // BM25 highlight back to a specific message inside a window doc
  // (telegram today). Absent for semantic-only hits; the frontend
  // switches the card into a bookended-window preview in that case.
  match_source_id?: string;
  match_message_id?: number;
  match_created_at?: string;
  match_sender_id?: number;
  match_sender_name?: string;
  match_avatar_key?: string;
}

// MessageLine mirrors the Go per-line metadata entry stored on
// telegram window docs. Consumed by the search card for bookended
// semantic-fallback rendering (and, in principle, by any future
// consumer that wants per-message preview data without refetching
// per-message docs).
export interface MessageLine {
  id: number;
  text: string;
  created_at: string;
  sender_id?: number;
  sender_name?: string;
  sender_username?: string;
  sender_avatar_key?: string;
}

export type Facet = Req<Schemas["github_com_muty_nexus_internal_model.Facet"]>;

export interface SearchResult {
  documents: DocumentHit[] | null;
  total_count: number;
  query: string;
  facets?: Record<string, Facet[]>;
}

export interface SearchFilters {
  sources?: string[];
  source_names?: string[];
  date_from?: string;
  date_to?: string;
}

// Related

export interface RelatedEdge {
  relation: Relation;
  document?: Document;
}

export interface RelatedResponse {
  outgoing: RelatedEdge[];
  incoming: RelatedEdge[];
}

// Conversations

export interface ConversationMessagesResponse {
  messages: Document[];
  next_before?: string;
  next_after?: string;
}

// Identities — the "who am I on each connected source" map.

export interface Identity {
  connector_id: string;
  source_type: string;
  source_name: string;
  external_id: string;
  external_name: string;
  has_avatar: boolean;
}

export interface IdentitiesResponse {
  identities: Identity[];
}

// Connectors

export type ConnectorConfig = Req<Schemas["internal_api.connectorResponse"]> & {
  config: Record<string, unknown>;
};

// Sync jobs + history.
//
// The backend emits four terminal statuses plus "running". Swagger treats
// `status` as a generic string; we narrow it here so components can use
// exhaustive switches and code-drive styling (e.g. status-lamp color).

// "interrupted" is set by the startup sweep (store.MarkInterruptedStuckRuns)
// when the process crashed or restarted mid-sync. Distinct from "failed"
// so the Activity timeline can style it muted rather than loud.
export type SyncStatus =
  | "running"
  | "completed"
  | "failed"
  | "canceled"
  | "interrupted";

export type SyncJob = Omit<Req<Schemas["internal_api.SyncJob"]>, "status"> & {
  status: SyncStatus;
};

export type SyncRun = Omit<
  Req<Schemas["github_com_muty_nexus_internal_model.SyncRun"]>,
  "status" | "completed_at"
> & {
  status: SyncStatus;
  // completed_at is null while the run is still in progress. Swag drops
  // the `omitempty`/nullable info — restore it as optional.
  completed_at?: string;
};

// Admin (Phase 4) types
//
// Swag still emits `?` on every field even though the handlers always
// populate them, and it dumps the per-source array as the per-item shape.
// The curated views tighten both.

export interface AdminEngineStats {
  enabled: boolean;
  provider: string;
  model: string;
  dimension?: number;
}

export interface AdminPerSourceStats {
  source_type: string;
  source_name: string;
  document_count: number;
  chunk_count: number;
  latest_indexed_at?: string;
  first_indexed_at?: string;
  cache_count: number;
  cache_bytes: number;
}

export interface AdminStats {
  total_documents: number;
  total_chunks: number;
  users_count: number;
  per_source: AdminPerSourceStats[];
  embedding: AdminEngineStats;
  rerank: AdminEngineStats;
}

// Settings: embedding / rerank (already on the BE but typed here for the
// rewrite). API keys arrive masked ("****abcd"); blank + "Replace" on the
// frontend asks for a new plaintext key to send back.
export interface EmbeddingSettings {
  provider: "" | "ollama" | "openai" | "voyage" | "cohere";
  model: string;
  api_key: string;
  ollama_url: string;
}

export interface RerankSettings {
  provider: "" | "voyage" | "cohere";
  model: string;
  api_key: string;
  // Post-rerank score floor. Docs below this are dropped. In [0,1].
  min_score: number;
}

// Retention: the sweeper reads these three keys every tick. The BE reports
// `min_sweep_interval_minutes` as a hard floor the admin can't submit below.
export interface RetentionSettings {
  retention_days: number;
  retention_per_connector: number;
  sweep_interval_minutes: number;
  min_sweep_interval_minutes: number;
}

export interface RetentionSettingsUpdate {
  retention_days: number;
  retention_per_connector: number;
  sweep_interval_minutes: number;
}

// Ranking knobs. The RankingManager loads these at boot and hot-swaps on
// every PUT — changes apply to the next query. The rerank-min-score
// scalar lives on RerankSettings (below) since it's only meaningful when a
// reranker is configured.
export interface RankingSettings {
  source_half_life_days: Record<string, number>;
  source_recency_floor: Record<string, number>;
  source_trust_weight: Record<string, number>;
  metadata_bonus_enabled: boolean;
  source_trust_enabled: boolean;
  known_source_types?: string[];
}

// Storage stats already exposed — curated here so hooks in Phase 4 can
// type-lift from api-client without re-deriving the shape.
export interface BinaryStoreStats {
  source_type: string;
  source_name: string;
  count: number;
  total_size: number;
}

export interface StorageWipeResult {
  deleted_count: number;
  bytes_freed: number;
}
