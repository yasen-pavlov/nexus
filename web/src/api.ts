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
  connector_id: string;
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
  shared: boolean;
  user_id?: string | null;
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
  shared?: boolean;
}

interface APIResponse<T> {
  data?: T;
  error?: string;
}

const TOKEN_KEY = 'nexus_jwt';

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

// Listeners notified when the API receives a 401 (e.g., expired/invalid token).
type UnauthorizedListener = () => void;
const unauthorizedListeners = new Set<UnauthorizedListener>();

export function onUnauthorized(listener: UnauthorizedListener): () => void {
  unauthorizedListeners.add(listener);
  return () => unauthorizedListeners.delete(listener);
}

function notifyUnauthorized(): void {
  unauthorizedListeners.forEach((fn) => fn());
}

async function fetchAPI<T>(url: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers);
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const res = await fetch(url, { ...options, headers });
  if (res.status === 401) {
    clearToken();
    notifyUnauthorized();
    throw new Error('Unauthorized');
  }
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

export async function triggerSync(connectorID: string): Promise<SyncJob> {
  return fetchAPI<SyncJob>(`/api/sync/${connectorID}`, { method: 'POST' });
}

export async function syncAll(): Promise<SyncJob[]> {
  return fetchAPI<SyncJob[]>('/api/sync', { method: 'POST' });
}

export async function deleteAllCursors(): Promise<void> {
  await fetchAPI('/api/sync/cursors', { method: 'DELETE' });
}

export async function deleteCursor(connectorID: string): Promise<void> {
  await fetchAPI(`/api/sync/cursors/${connectorID}`, { method: 'DELETE' });
}

export async function triggerReindex(): Promise<{ message: string; dimension: number; connectors: number }> {
  return fetchAPI('/api/reindex', { method: 'POST' });
}

export function streamSyncProgress(
  connectorID: string,
  onUpdate: (job: SyncJob) => void,
  onDone: () => void,
): () => void {
  // EventSource cannot set custom headers, so the token is passed as a query
  // param. The auth middleware accepts both Authorization and ?token= for SSE.
  const token = getToken();
  const url = token
    ? `/api/sync/${connectorID}/progress?token=${encodeURIComponent(token)}`
    : `/api/sync/${connectorID}/progress`;
  const es = new EventSource(url);
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
  const headers = new Headers();
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const res = await fetch(`/api/connectors/${id}`, { method: 'DELETE', headers });
  if (res.status === 401) {
    clearToken();
    notifyUnauthorized();
    throw new Error('Unauthorized');
  }
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

// Auth

export interface User {
  id: string;
  username: string;
  role: 'admin' | 'user';
}

export interface AuthResponse {
  token: string;
  user: User;
}

export interface HealthResponse {
  status: string;
  setup_required?: boolean;
}

export async function getHealth(): Promise<HealthResponse> {
  return fetchAPI<HealthResponse>('/api/health');
}

export async function register(username: string, password: string): Promise<AuthResponse> {
  return fetchAPI<AuthResponse>('/api/auth/register', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
}

export async function login(username: string, password: string): Promise<AuthResponse> {
  return fetchAPI<AuthResponse>('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
}

export async function getMe(): Promise<User> {
  return fetchAPI<User>('/api/auth/me');
}

export async function listUsers(): Promise<User[]> {
  return fetchAPI<User[]>('/api/users');
}

export async function createUser(username: string, password: string, role: 'admin' | 'user'): Promise<User> {
  return fetchAPI<User>('/api/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, role }),
  });
}

export async function deleteUser(id: string): Promise<void> {
  const headers = new Headers();
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const res = await fetch(`/api/users/${id}`, { method: 'DELETE', headers });
  if (res.status === 401) {
    clearToken();
    notifyUnauthorized();
    throw new Error('Unauthorized');
  }
  if (!res.ok && res.status !== 204) {
    const body = await res.json();
    throw new Error(body.error || 'Delete failed');
  }
}

export async function changePassword(userID: string, password: string): Promise<void> {
  const headers = new Headers({ 'Content-Type': 'application/json' });
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const res = await fetch(`/api/users/${userID}/password`, {
    method: 'PUT',
    headers,
    body: JSON.stringify({ password }),
  });
  if (res.status === 401) {
    clearToken();
    notifyUnauthorized();
    throw new Error('Unauthorized');
  }
  if (!res.ok && res.status !== 204) {
    const body = await res.json();
    throw new Error(body.error || 'Change password failed');
  }
}

// Rerank settings

export interface RerankSettings {
  provider: string;
  model: string;
  api_key: string;
}

export async function getRerankSettings(): Promise<RerankSettings> {
  return fetchAPI<RerankSettings>('/api/settings/rerank');
}

export async function updateRerankSettings(settings: RerankSettings): Promise<RerankSettings> {
  return fetchAPI<RerankSettings>('/api/settings/rerank', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
}
