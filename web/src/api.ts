export interface Document {
  id: string;
  source_type: string;
  source_name: string;
  source_id: string;
  title: string;
  content: string;
  metadata: Record<string, unknown>;
  url: string;
  visibility: string;
  created_at: string;
  indexed_at: string;
}

export interface DocumentHit extends Document {
  rank: number;
  headline: string;
}

export interface SearchResult {
  documents: DocumentHit[] | null;
  total_count: number;
  query: string;
}

export interface SyncReport {
  connector_name: string;
  connector_type: string;
  docs_processed: number;
  errors: number;
  duration: number;
}

export interface ConnectorConfig {
  id: string;
  type: string;
  name: string;
  config: Record<string, unknown>;
  enabled: boolean;
  schedule: string;
  last_run: string | null;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface CreateConnectorRequest {
  type: string;
  name: string;
  config: Record<string, unknown>;
  enabled: boolean;
  schedule: string;
}

interface APIResponse<T> {
  data?: T;
  error?: string;
}

async function fetchAPI<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, options);
  const body: APIResponse<T> = await res.json();
  if (body.error) {
    throw new Error(body.error);
  }
  return body.data as T;
}

export async function search(query: string, limit = 20, offset = 0): Promise<SearchResult> {
  const params = new URLSearchParams({ q: query, limit: String(limit), offset: String(offset) });
  return fetchAPI<SearchResult>(`/api/search?${params}`);
}

export async function triggerSync(connector: string): Promise<SyncReport> {
  return fetchAPI<SyncReport>(`/api/sync/${connector}`, { method: 'POST' });
}

export async function listConnectors(): Promise<ConnectorConfig[]> {
  return fetchAPI<ConnectorConfig[]>('/api/connectors/');
}

export async function getConnector(id: string): Promise<ConnectorConfig> {
  return fetchAPI<ConnectorConfig>(`/api/connectors/${id}`);
}

export async function createConnector(req: CreateConnectorRequest): Promise<ConnectorConfig> {
  return fetchAPI<ConnectorConfig>('/api/connectors/', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
}

export async function updateConnector(id: string, req: CreateConnectorRequest): Promise<ConnectorConfig> {
  return fetchAPI<ConnectorConfig>(`/api/connectors/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
}

export async function deleteConnector(id: string): Promise<void> {
  const res = await fetch(`/api/connectors/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const body = await res.json();
    throw new Error(body.error || 'Delete failed');
  }
}
