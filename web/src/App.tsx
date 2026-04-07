import { useState, useEffect, useRef, useCallback } from 'react';
import { search, triggerSync, streamSyncProgress, listConnectors, listSyncJobs, type SearchResult, type SyncJob, type ConnectorConfig, type SearchFilters } from './api';
import ConnectorManager from './ConnectorManager';
import SearchFiltersBar from './SearchFilters';
import Settings from './Settings';
import './App.css';

function App() {
  const [query, setQuery] = useState('');
  const [result, setResult] = useState<SearchResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [syncJobs, setSyncJobs] = useState<Record<string, SyncJob>>({});
  const [connectors, setConnectors] = useState<ConnectorConfig[]>([]);
  const [error, setError] = useState('');
  const [showConnectors, setShowConnectors] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [filters, setFilters] = useState<SearchFilters>({});
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const cleanupRefs = useRef<Record<string, () => void>>({});

  const loadConnectors = useCallback(() => {
    listConnectors().then(setConnectors).catch(() => {});
  }, []);

  useEffect(loadConnectors, [loadConnectors]);

  // On mount: load any running sync jobs and resume SSE streams
  useEffect(() => {
    listSyncJobs().then((jobs) => {
      const running: Record<string, SyncJob> = {};
      for (const job of jobs) {
        if (job.status === 'running') {
          running[job.connector_name] = job;
          const cleanup = streamSyncProgress(
            job.connector_name,
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
  }, []);

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

  const handleSync = async (connectorName: string) => {
    setError('');
    try {
      const job = await triggerSync(connectorName);
      setSyncJobs((prev) => ({ ...prev, [connectorName]: job }));

      // Open SSE stream for progress
      const cleanup = streamSyncProgress(
        connectorName,
        (update) => setSyncJobs((prev) => ({ ...prev, [connectorName]: update })),
        () => {
          delete cleanupRefs.current[connectorName];
        },
      );
      cleanupRefs.current[connectorName] = cleanup;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sync failed');
    }
  };

  const dismissJob = (connectorName: string) => {
    setSyncJobs((prev) => {
      const next = { ...prev };
      delete next[connectorName];
      return next;
    });
  };

  if (showSettings) {
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

  return (
    <div className="app">
      <header className="header">
        <h1>Nexus</h1>
        <p className="subtitle">Personal Search</p>
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
        {connectors.filter(c => c.enabled).map((conn) => {
          const job = syncJobs[conn.name];
          const isRunning = job?.status === 'running';
          return (
            <button
              key={conn.name}
              className="sync-button"
              onClick={() => handleSync(conn.name)}
              disabled={isRunning}
            >
              {isRunning ? `Syncing ${conn.name}...` : `Sync ${conn.name}`}
            </button>
          );
        })}
        <button
          className="sync-button cm-settings-btn"
          onClick={() => setShowConnectors(true)}
        >
          Connectors
        </button>
        <button
          className="sync-button cm-settings-btn"
          onClick={() => setShowSettings(true)}
        >
          Settings
        </button>
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
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
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
