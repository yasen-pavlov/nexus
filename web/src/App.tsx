import { useState, useEffect, useRef, useCallback } from 'react';
import { search, triggerSync, syncAll, deleteAllCursors, triggerReindex, streamSyncProgress, listConnectors, listSyncJobs, downloadDocument, type SearchResult, type SyncJob, type ConnectorConfig, type SearchFilters, type Document } from './api';
import ConnectorManager from './ConnectorManager';
import SearchFiltersBar from './SearchFilters';
import Settings from './Settings';
import Login from './Login';
import UserManagement from './UserManagement';
import RelatedFooter from './RelatedFooter';
import ChatBrowser from './ChatBrowser';
import { useAuth } from './AuthContext';
import './App.css';

interface ChatBrowserTarget {
  sourceType: string;
  conversationID: string;
  anchorMessageID?: number;
}

function App() {
  const { user, loading: authLoading, logout } = useAuth();
  const [query, setQuery] = useState('');
  const [result, setResult] = useState<SearchResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [syncJobs, setSyncJobs] = useState<Record<string, SyncJob>>({});
  const [connectors, setConnectors] = useState<ConnectorConfig[]>([]);
  const [error, setError] = useState('');
  const [showConnectors, setShowConnectors] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [showUsers, setShowUsers] = useState(false);
  const [chatBrowser, setChatBrowser] = useState<ChatBrowserTarget | null>(null);
  const isAdmin = user?.role === 'admin';
  const canModify = (conn: ConnectorConfig) =>
    isAdmin || (!!conn.user_id && conn.user_id === user?.id);
  const [filters, setFilters] = useState<SearchFilters>({});
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const cleanupRefs = useRef<Record<string, () => void>>({});

  const loadConnectors = useCallback(() => {
    if (!user) return;
    listConnectors().then(setConnectors).catch(() => {});
  }, [user]);

  useEffect(loadConnectors, [loadConnectors]);

  // On mount: load any running sync jobs and resume SSE streams
  useEffect(() => {
    if (!user) return;
    listSyncJobs().then((jobs) => {
      const running: Record<string, SyncJob> = {};
      for (const job of jobs) {
        if (job.status === 'running') {
          running[job.connector_name] = job;
          const cleanup = streamSyncProgress(
            job.connector_id,
            (update) => setSyncJobs((prev) => ({ ...prev, [job.connector_name]: update })),
            () => { delete cleanupRefs.current[job.connector_name]; },
          );
          cleanupRefs.current[job.connector_name] = cleanup;
        }
      }
      if (Object.keys(running).length > 0) {
        setSyncJobs(running);
      }
    }).catch(() => {});
  }, [user]);

  // Cleanup SSE connections on unmount
  useEffect(() => {
    return () => {
      Object.values(cleanupRefs.current).forEach((fn) => fn());
    };
  }, []);

  const doSearch = useCallback(async (q: string, f?: SearchFilters) => {
    if (!q.trim()) {
      setResult(null);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const res = await search(q, 20, 0, f);
      setResult(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Search failed');
    } finally {
      setLoading(false);
    }
  }, []);

  const handleInputChange = (value: string) => {
    setQuery(value);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => doSearch(value, filters), 300);
  };

  const handleFilterChange = (newFilters: SearchFilters) => {
    setFilters(newFilters);
    if (query.trim()) {
      doSearch(query, newFilters);
    }
  };

  const handleSync = async (conn: ConnectorConfig) => {
    setError('');
    try {
      const job = await triggerSync(conn.id);
      setSyncJobs((prev) => ({ ...prev, [conn.name]: job }));

      // Open SSE stream for progress
      const cleanup = streamSyncProgress(
        conn.id,
        (update) => setSyncJobs((prev) => ({ ...prev, [conn.name]: update })),
        () => {
          delete cleanupRefs.current[conn.name];
        },
      );
      cleanupRefs.current[conn.name] = cleanup;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sync failed');
    }
  };

  const handleSyncAll = async () => {
    setError('');
    try {
      const jobs = (await syncAll()) ?? [];
      const newJobs: Record<string, SyncJob> = {};
      for (const job of jobs) {
        newJobs[job.connector_name] = job;
        const cleanup = streamSyncProgress(
          job.connector_id,
          (update) => setSyncJobs((prev) => ({ ...prev, [job.connector_name]: update })),
          () => { delete cleanupRefs.current[job.connector_name]; },
        );
        cleanupRefs.current[job.connector_name] = cleanup;
      }
      setSyncJobs((prev) => ({ ...prev, ...newJobs }));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sync all failed');
    }
  };

  const handleResetCursors = async () => {
    setError('');
    try {
      await deleteAllCursors();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reset cursors failed');
    }
  };

  const handleReindex = async () => {
    setError('');
    try {
      await triggerReindex();
      // After reindex, all connectors sync — load their progress
      const jobs = await listSyncJobs();
      const running: Record<string, SyncJob> = {};
      for (const job of jobs) {
        if (job.status === 'running') {
          running[job.connector_name] = job;
          const cleanup = streamSyncProgress(
            job.connector_id,
            (update) => setSyncJobs((prev) => ({ ...prev, [job.connector_name]: update })),
            () => { delete cleanupRefs.current[job.connector_name]; },
          );
          cleanupRefs.current[job.connector_name] = cleanup;
        }
      }
      setSyncJobs((prev) => ({ ...prev, ...running }));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reindex failed');
    }
  };

  const anySyncing = Object.values(syncJobs).some((j) => j.status === 'running');

  const dismissJob = (connectorName: string) => {
    setSyncJobs((prev) => {
      const next = { ...prev };
      delete next[connectorName];
      return next;
    });
  };

  if (authLoading) {
    return <div className="app"><div className="loading">Loading...</div></div>;
  }

  if (!user) {
    return <Login />;
  }

  if (showSettings && isAdmin) {
    return (
      <div className="app">
        <header className="header">
          <h1>Nexus</h1>
          <p className="subtitle">Personal Search</p>
        </header>
        <Settings onClose={() => setShowSettings(false)} />
      </div>
    );
  }

  if (showUsers && isAdmin) {
    return (
      <div className="app">
        <header className="header">
          <h1>Nexus</h1>
          <p className="subtitle">Personal Search</p>
        </header>
        <UserManagement onClose={() => setShowUsers(false)} />
      </div>
    );
  }

  if (showConnectors) {
    return (
      <div className="app">
        <header className="header">
          <h1>Nexus</h1>
          <p className="subtitle">Personal Search</p>
        </header>
        <ConnectorManager onClose={() => { setShowConnectors(false); loadConnectors(); }} />
      </div>
    );
  }

  if (chatBrowser) {
    return (
      <div className="app">
        <ChatBrowser
          sourceType={chatBrowser.sourceType}
          conversationID={chatBrowser.conversationID}
          anchorMessageID={chatBrowser.anchorMessageID}
          onClose={() => setChatBrowser(null)}
        />
      </div>
    );
  }

  // openNeighbor lands on a neighbor document. Telegram docs navigate
  // into the chat browser; other sources are surfaced by rerunning the
  // search with the neighbor's title as the query — crude but it does
  // the job for the PoC without an ID-based doc viewer.
  const openNeighbor = (doc: Document) => {
    if (doc.source_type === 'telegram' && doc.conversation_id) {
      const raw = doc.metadata?.message_id ?? doc.metadata?.anchor_message_id;
      const anchor = typeof raw === 'number' ? raw : undefined;
      setChatBrowser({ sourceType: 'telegram', conversationID: doc.conversation_id, anchorMessageID: anchor });
      return;
    }
    const q = doc.title || doc.source_id;
    setQuery(q);
    doSearch(q, filters);
  };

  return (
    <div className="app">
      <header className="header">
        <h1>Nexus</h1>
        <p className="subtitle">Personal Search</p>
        <div className="header-user">
          <span className="header-username">
            {user.username}{isAdmin && <span className="header-role"> · admin</span>}
          </span>
          <button className="header-logout" onClick={logout}>Sign out</button>
        </div>
      </header>

      <div className="search-container">
        <input
          type="text"
          className="search-input"
          placeholder="Search across all your data..."
          value={query}
          onChange={(e) => handleInputChange(e.target.value)}
          autoFocus
        />
      </div>

      <div className="controls">
        {connectors.filter(c => c.enabled && canModify(c)).map((conn) => {
          const job = syncJobs[conn.name];
          const isRunning = job?.status === 'running';
          return (
            <button
              key={conn.name}
              className="sync-button"
              onClick={() => handleSync(conn)}
              disabled={isRunning}
            >
              {isRunning ? `Syncing ${conn.name}...` : `Sync ${conn.name}`}
            </button>
          );
        })}
        {connectors.some(c => c.enabled && canModify(c)) && (
          <button
            className="sync-button"
            onClick={handleSyncAll}
            disabled={anySyncing}
          >
            Sync All
          </button>
        )}
        {isAdmin && (
          <button
            className="sync-button"
            onClick={handleResetCursors}
            disabled={anySyncing}
          >
            Reset Cursors
          </button>
        )}
        {isAdmin && (
          <button
            className="sync-button cm-btn-danger"
            onClick={handleReindex}
            disabled={anySyncing}
          >
            Re-index
          </button>
        )}
        <button
          className="sync-button cm-settings-btn"
          onClick={() => setShowConnectors(true)}
        >
          Connectors
        </button>
        {isAdmin && (
          <button
            className="sync-button cm-settings-btn"
            onClick={() => setShowSettings(true)}
          >
            Settings
          </button>
        )}
        {isAdmin && (
          <button
            className="sync-button cm-settings-btn"
            onClick={() => setShowUsers(true)}
          >
            Users
          </button>
        )}
      </div>

      {Object.entries(syncJobs).map(([name, job]) => (
        <SyncProgress key={name} job={job} onDismiss={() => dismissJob(name)} />
      ))}

      {result && (
        <SearchFiltersBar
          facets={result.facets}
          filters={filters}
          onChange={handleFilterChange}
        />
      )}

      {error && <div className="error">{error}</div>}

      {loading && <div className="loading">Searching...</div>}

      {result && (
        <div className="results">
          <div className="results-count">
            {result.total_count} result{result.total_count !== 1 ? 's' : ''} for "{result.query}"
          </div>
          {result.documents?.map((hit) => (
            <div key={hit.id} className="result-card">
              <div className="result-header">
                {hit.url && !hit.url.startsWith('file://') ? (
                  <a className="result-title" href={hit.url} target="_blank" rel="noopener noreferrer">{hit.title}</a>
                ) : (
                  <span className="result-title">{hit.title}</span>
                )}
                <span className="result-source">{hit.source_type}:{hit.source_name}</span>
              </div>
              {hit.headline ? (
                <div
                  className="result-snippet"
                  dangerouslySetInnerHTML={{ __html: hit.headline }}
                />
              ) : hit.content ? (
                <div className="result-snippet">
                  {hit.content.length > 200 ? hit.content.slice(0, 200) + '...' : hit.content}
                </div>
              ) : null}
              <div className="result-meta">
                {hit.metadata?.path ? <span className="result-path">{String(hit.metadata.path)}</span> : null}
                <span className="result-date">
                  {new Date(hit.created_at).toLocaleDateString()}
                </span>
                {hit.mime_type ? (
                  <button
                    type="button"
                    className="result-download"
                    title={`Download (${formatBytes(hit.size)})`}
                    onClick={() => {
                      downloadDocument(hit.id, hit.title).catch((err) => setError(err.message));
                    }}
                  >
                    ↓ Download
                  </button>
                ) : null}
                {hit.source_type === 'telegram' && hit.conversation_id ? (
                  <button
                    type="button"
                    className="result-download"
                    onClick={() => {
                      const raw = hit.metadata?.anchor_message_id ?? hit.metadata?.message_id;
                      const anchor = typeof raw === 'number' ? raw : undefined;
                      setChatBrowser({ sourceType: 'telegram', conversationID: hit.conversation_id!, anchorMessageID: anchor });
                    }}
                  >
                    💬 Open in chat
                  </button>
                ) : null}
              </div>
              <RelatedFooter docID={hit.id} onNavigate={openNeighbor} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let n = bytes;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

function SyncProgress({ job, onDismiss }: { job: SyncJob; onDismiss: () => void }) {
  const isRunning = job.status === 'running';
  const isFailed = job.status === 'failed';
  const percent = job.docs_total > 0 ? Math.round((job.docs_processed / job.docs_total) * 100) : 0;

  return (
    <div className={`sync-progress ${isFailed ? 'sync-progress-error' : ''}`}>
      <div className="sync-progress-header">
        <span className="sync-progress-label">
          {isRunning
            ? `Syncing ${job.connector_name}...`
            : isFailed
              ? `Sync failed: ${job.connector_name}`
              : `Synced ${job.connector_name}`}
        </span>
        <span className="sync-progress-stats">
          {job.docs_processed}{job.docs_total > 0 ? `/${job.docs_total}` : ''} docs
          {job.docs_deleted > 0 && `, ${job.docs_deleted} deleted`}
          {job.errors > 0 && ` (${job.errors} errors)`}
        </span>
        {!isRunning && (
          <button className="sync-progress-dismiss" onClick={onDismiss}>&times;</button>
        )}
      </div>
      {isRunning && (
        <div className="sync-progress-bar-bg">
          <div className="sync-progress-bar-fill" style={{ width: `${percent}%` }} />
        </div>
      )}
      {isFailed && job.error && (
        <div className="sync-progress-error-msg">{job.error}</div>
      )}
    </div>
  );
}

export default App;
