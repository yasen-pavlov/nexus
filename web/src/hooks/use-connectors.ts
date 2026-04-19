import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";

import { fetchAPI } from "@/lib/api-client";
import type { ConnectorConfig, SyncRun } from "@/lib/api-types";
import { connectorKeys } from "@/lib/query-keys";

export interface CreateConnectorInput {
  type: string;
  name: string;
  config: Record<string, unknown>;
  enabled: boolean;
  schedule: string;
  shared: boolean;
}

export interface UpdateConnectorInput extends CreateConnectorInput {
  id: string;
}

/**
 * List + CRUD for connectors. Mutations invalidate the list query so the
 * specimen cards re-render with fresh metadata. Delete additionally
 * navigates back to the list page from the detail route.
 */
export function useConnectors() {
  const queryClient = useQueryClient();

  const list = useQuery<ConnectorConfig[]>({
    queryKey: connectorKeys.list(),
    queryFn: () => fetchAPI<ConnectorConfig[]>("/api/connectors/"),
    staleTime: 30_000,
  });

  const create = useMutation({
    mutationFn: (input: CreateConnectorInput) =>
      fetchAPI<ConnectorConfig>("/api/connectors/", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: connectorKeys.list() });
    },
  });

  const update = useMutation({
    mutationFn: ({ id, ...body }: UpdateConnectorInput) =>
      fetchAPI<ConnectorConfig>(`/api/connectors/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: connectorKeys.list() });
      queryClient.invalidateQueries({ queryKey: connectorKeys.detail(variables.id) });
    },
  });

  const remove = useMutation({
    mutationFn: (id: string) =>
      fetchAPI<void>(`/api/connectors/${id}`, { method: "DELETE" }),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: connectorKeys.list() });
      queryClient.removeQueries({ queryKey: connectorKeys.detail(id) });
    },
  });

  return {
    connectors: list.data ?? [],
    isLoading: list.isPending,
    error: list.error,
    createConnector: create.mutateAsync,
    updateConnector: update.mutateAsync,
    deleteConnector: remove.mutateAsync,
  };
}

/**
 * Single-connector detail + activity-timeline. Composes two queries so
 * the detail page can render the header immediately while runs are
 * still loading.
 */
export function useConnector(id: string) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const detail = useQuery<ConnectorConfig>({
    queryKey: connectorKeys.detail(id),
    queryFn: () => fetchAPI<ConnectorConfig>(`/api/connectors/${id}`),
    staleTime: 30_000,
    enabled: !!id,
  });

  const runs = useQuery<SyncRun[]>({
    queryKey: connectorKeys.runs(id),
    queryFn: () => fetchAPI<SyncRun[]>(`/api/connectors/${id}/runs?limit=50`),
    staleTime: 10_000,
    refetchOnWindowFocus: false,
    enabled: !!id,
  });

  const update = useMutation({
    mutationFn: (input: CreateConnectorInput) =>
      fetchAPI<ConnectorConfig>(`/api/connectors/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: connectorKeys.detail(id) });
      queryClient.invalidateQueries({ queryKey: connectorKeys.list() });
    },
  });

  const remove = useMutation({
    mutationFn: () => fetchAPI<void>(`/api/connectors/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: connectorKeys.list() });
      queryClient.removeQueries({ queryKey: connectorKeys.detail(id) });
      navigate({ to: "/connectors" });
    },
  });

  return {
    connector: detail.data,
    runs: runs.data ?? [],
    isLoading: detail.isPending,
    error: detail.error,
    runsLoading: runs.isPending,
    updateConnector: update.mutateAsync,
    deleteConnector: remove.mutateAsync,
  };
}
