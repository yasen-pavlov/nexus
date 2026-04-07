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

export interface Facet {
  value: string;
  count: number;
}

export interface SearchResult {
  documents: DocumentHit[] | null;
  total_count: number;
  query: string;
  facets?: Record<string, Facet[]>;
}

export interface SearchFilters {
  sources?: string[];
  sourceNames?: string[];
  dateFrom?: string;
  dateTo?: string;
}

export interface SyncReport {
  connector_name: string;
  connector_type: string;
  docs_processed: number;
  errors: number;
  duration: number;
}

export interface SyncJob {
  id: string;
  connector_name: string;
  connector_type: string;
  status: 'running' | 'completed' | 'failed';
  docs_total: number;
  docs_processed: number;
  errors: number;
  error?: string;
  started_at: string;
  completed_at?: string;
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

export async function search(query: string, limit = 20, offset = 0, filters?: SearchFilters): Promise<SearchResult> {
  const params = new URLSearchParams({ q: query, limit: String(limit), offset: String(offset) });
  if (filters?.sources?.length) params.set('sources', filters.sources.join(','));
  if (filters?.sourceNames?.length) params.set('source_names', filters.sourceNames.join(','));
  if (filters?.dateFrom) params.set('date_from', filters.dateFrom);
  if (filters?.dateTo) params.set('date_to', filters.dateTo);
  return fetchAPI<SearchResult>(`/api/search?${params}`);
}

export async function triggerSync(connector: string): Promise<SyncJob> {
  return fetchAPI<SyncJob>(`/api/sync/${connector}`, { method: 'POST' });
}

export function streamSyncProgress(
  connector: string,
  onUpdate: (job: SyncJob) => void,
  onDone: () => void,
): () => void {
  const es = new EventSource(`/api/sync/${connector}/progress`);
  es.onmessage = (e) => onUpdate(JSON.parse(e.data));
  es.addEventListener('done', () => { es.close(); onDone(); });
  es.onerror = () => { es.close(); onDone(); };
  return () => es.close();
}

export async function listSyncJobs(): Promise<SyncJob[]> {
  return fetchAPI<SyncJob[]>('/api/sync');
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

export async function telegramAuthStart(connectorId: string): Promise<{ status: string; message: string }> {
  return fetchAPI(`/api/connectors/${connectorId}/auth/start`, { method: 'POST' });
}

export async function telegramAuthCode(connectorId: string, code: string, password?: string): Promise<{ status: string }> {
  return fetchAPI(`/api/connectors/${connectorId}/auth/code`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code, password }),
  });
}

export async function deleteConnector(id: string): Promise<void> {
  const res = await fetch(`/api/connectors/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const body = await res.json();
    throw new Error(body.error || 'Delete failed');
  }
}

// Embedding settings

export interface EmbeddingSettings {
  provider: string;
  model: string;
  api_key: string;
  ollama_url: string;
}

export async function getEmbeddingSettings(): Promise<EmbeddingSettings> {
  return fetchAPI<EmbeddingSettings>('/api/settings/embedding');
}

export async function updateEmbeddingSettings(settings: EmbeddingSettings): Promise<EmbeddingSettings> {
  return fetchAPI<EmbeddingSettings>('/api/settings/embedding', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
}
