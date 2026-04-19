import type { SearchFilters } from "./api-types";

export const authKeys = {
  all: ["auth"] as const,
  me: () => [...authKeys.all, "me"] as const,
  health: () => [...authKeys.all, "health"] as const,
};

export const connectorKeys = {
  all: ["connectors"] as const,
  list: () => [...connectorKeys.all, "list"] as const,
  detail: (id: string) => [...connectorKeys.all, "detail", id] as const,
  runs: (id: string) => [...connectorKeys.all, "runs", id] as const,
};

export const syncKeys = {
  all: ["sync"] as const,
  jobs: () => [...syncKeys.all, "jobs"] as const,
};

export const searchKeys = {
  all: ["search"] as const,
  query: (q: string, filters: SearchFilters) =>
    [...searchKeys.all, q, filters] as const,
};

export const documentKeys = {
  all: ["documents"] as const,
  related: (id: string) => [...documentKeys.all, id, "related"] as const,
  bySource: (sourceType: string, sourceID: string) =>
    [...documentKeys.all, "by-source", sourceType, sourceID] as const,
};

export const conversationKeys = {
  all: ["conversations"] as const,
  messages: (sourceType: string, conversationID: string, anchorTs?: string) =>
    [
      ...conversationKeys.all,
      sourceType,
      conversationID,
      anchorTs ?? "tail",
    ] as const,
};

export const identityKeys = {
  all: ["identities"] as const,
  list: () => [...identityKeys.all, "list"] as const,
};

export const avatarKeys = {
  all: ["avatars"] as const,
  blob: (connectorID: string, externalID: string) =>
    [...avatarKeys.all, connectorID, externalID] as const,
};

export const adminKeys = {
  all: ["admin"] as const,
  stats: () => [...adminKeys.all, "stats"] as const,
};

export const settingsKeys = {
  all: ["settings"] as const,
  embedding: () => [...settingsKeys.all, "embedding"] as const,
  rerank: () => [...settingsKeys.all, "rerank"] as const,
  retention: () => [...settingsKeys.all, "retention"] as const,
  ranking: () => [...settingsKeys.all, "ranking"] as const,
};

export const userKeys = {
  all: ["users"] as const,
  list: () => [...userKeys.all, "list"] as const,
};

export const storageKeys = {
  all: ["storage"] as const,
  stats: () => [...storageKeys.all, "stats"] as const,
};
