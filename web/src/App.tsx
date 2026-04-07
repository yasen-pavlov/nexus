import { useState, useEffect, useRef, useCallback } from 'react';
import { search, triggerSync, listConnectors, type SearchResult, type SyncReport, type ConnectorConfig, type SearchFilters } from './api';
import ConnectorManager from './ConnectorManager';
import SearchFiltersBar from './SearchFilters';
import Settings from './Settings';
import './App.css';

function App() {
  const [query, setQuery] = useState('');
  const [result, setResult] = useState<SearchResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [syncReport, setSyncReport] = useState<SyncReport | null>(null);
  const [connectors, setConnectors] = useState<ConnectorConfig[]>([]);
  const [error, setError] = useState('');
  const [showConnectors, setShowConnectors] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [filters, setFilters] = useState<SearchFilters>({});
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const loadConnectors = useCallback(() => {
    listConnectors().then(setConnectors).catch(() => {});
  }, []);

  useEffect(loadConnectors, [loadConnectors]);

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
    setSyncing(true);
    setSyncReport(null);
    setError('');
    try {
      const report = await triggerSync(connectorName);
      setSyncReport(report);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sync failed');
    } finally {
      setSyncing(false);
    }
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
        {connectors.filter(c => c.enabled).map((conn) => (
          <button
            key={conn.name}
            className="sync-button"
            onClick={() => handleSync(conn.name)}
            disabled={syncing}
          >
            {syncing ? 'Syncing...' : `Sync ${conn.name}`}
          </button>
        ))}
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

      {syncReport && (
        <div className="sync-report">
          Synced {syncReport.docs_processed} documents from {syncReport.connector_name}
          {syncReport.errors > 0 && ` (${syncReport.errors} errors)`}
        </div>
      )}

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
                <span className="result-title">{hit.title}</span>
                <span className="result-source">{hit.source_type}:{hit.source_name}</span>
              </div>
              <div
                className="result-snippet"
                dangerouslySetInnerHTML={{ __html: hit.headline }}
              />
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

export default App;
