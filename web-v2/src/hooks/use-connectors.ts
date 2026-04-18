import { useQuery } from "@tanstack/react-query";
import { fetchAPI } from "@/lib/api-client";
import type { ConnectorConfig } from "@/lib/api-types";
import { connectorKeys } from "@/lib/query-keys";

export function useConnectors() {
  return useQuery({
    queryKey: connectorKeys.list(),
    queryFn: () => fetchAPI<ConnectorConfig[]>("/api/connectors/"),
  });
}
