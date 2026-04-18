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

export type User = Omit<Req<Schemas["internal_api.userResponse"]>, "role"> & {
  role: "admin" | "user";
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

// Connectors

export type ConnectorConfig = Req<Schemas["internal_api.connectorResponse"]> & {
  config: Record<string, unknown>;
};
