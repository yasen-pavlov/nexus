export const authKeys = {
  all: ["auth"] as const,
  me: () => [...authKeys.all, "me"] as const,
  health: () => [...authKeys.all, "health"] as const,
};

export const connectorKeys = {
  all: ["connectors"] as const,
  list: () => [...connectorKeys.all, "list"] as const,
  detail: (id: string) => [...connectorKeys.all, "detail", id] as const,
};

export const searchKeys = {
  all: ["search"] as const,
  query: (q: string, filters?: Record<string, unknown>) =>
    [...searchKeys.all, q, filters] as const,
};
