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

export interface ConnectorInfo {
  name: string;
  type: string;
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

export async function listConnectors(): Promise<ConnectorInfo[]> {
  return fetchAPI<ConnectorInfo[]>('/api/connectors');
}
