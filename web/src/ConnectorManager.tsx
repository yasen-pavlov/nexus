import { useState, useEffect } from 'react';
import {
  listConnectors,
  createConnector,
  updateConnector,
  deleteConnector,
  triggerSync,
  telegramAuthStart,
  telegramAuthCode,
  type ConnectorConfig,
  type CreateConnectorRequest,
  type SyncReport,
} from './api';

interface Props {
  onClose: () => void;
}

const CONNECTOR_TYPES = ['filesystem', 'paperless', 'telegram'];

const COMMON_FIELDS = [
  { key: 'sync_since_days', label: 'Sync History (days)', placeholder: 'e.g., 30 (empty = all history)' },
  { key: 'sync_since', label: 'Sync Since Date', placeholder: 'YYYY-MM-DD (overridden by days if set)' },
];

const CONFIG_FIELDS: Record<string, { key: string; label: string; placeholder: string }[]> = {
  filesystem: [
    { key: 'root_path', label: 'Root Path', placeholder: '/data/files' },
    { key: 'patterns', label: 'File Patterns', placeholder: '*.txt,*.md' },
    ...COMMON_FIELDS,
  ],
  paperless: [
    { key: 'url', label: 'Paperless URL', placeholder: 'http://paperless:8000' },
    { key: 'token', label: 'API Token', placeholder: 'your-api-token' },
    ...COMMON_FIELDS,
  ],
  telegram: [
    { key: 'api_id', label: 'API ID', placeholder: 'From my.telegram.org' },
    { key: 'api_hash', label: 'API Hash', placeholder: 'From my.telegram.org' },
    { key: 'phone', label: 'Phone Number', placeholder: '+1234567890' },
    { key: 'chat_filter', label: 'Chat Filter', placeholder: 'Chat names or IDs (comma-separated, empty = all)' },
    ...COMMON_FIELDS,
  ],
};

export default function ConnectorManager({ onClose }: Props) {
  const [connectors, setConnectors] = useState<ConnectorConfig[]>([]);
  const [editing, setEditing] = useState<ConnectorConfig | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');
  const [syncReport, setSyncReport] = useState<SyncReport | null>(null);
  const [syncing, setSyncing] = useState('');
  const [authConnectorId, setAuthConnectorId] = useState('');
  const [authCode, setAuthCode] = useState('');
  const [authPassword, setAuthPassword] = useState('');
  const [authStatus, setAuthStatus] = useState('');

  const loadConnectors = () => {
    listConnectors().then(setConnectors).catch((err) => setError(err.message));
  };

  useEffect(loadConnectors, []);

  const handleCreate = async (req: CreateConnectorRequest) => {
    setError('');
    try {
      await createConnector(req);
      setCreating(false);
      loadConnectors();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Create failed');
    }
  };

  const handleUpdate = async (id: string, req: CreateConnectorRequest) => {
    setError('');
    try {
      await updateConnector(id, req);
      setEditing(null);
      loadConnectors();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Update failed');
    }
  };

  const handleDelete = async (id: string) => {
    setError('');
    try {
      await deleteConnector(id);
      loadConnectors();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed');
    }
  };

  const handleSync = async (name: string) => {
    setSyncing(name);
    setSyncReport(null);
    try {
      const report = await triggerSync(name);
      setSyncReport(report);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sync failed');
    } finally {
      setSyncing('');
    }
  };

  const handleAuthStart = async (connectorId: string) => {
    setError('');
    setAuthStatus('');
    try {
      const result = await telegramAuthStart(connectorId);
      setAuthConnectorId(connectorId);
      setAuthStatus(result.message || 'Code sent — check your Telegram app');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Auth start failed');
    }
  };

  const handleAuthCode = async () => {
    setError('');
    try {
      await telegramAuthCode(authConnectorId, authCode, authPassword || undefined);
      setAuthConnectorId('');
      setAuthCode('');
      setAuthPassword('');
      setAuthStatus('Connected successfully!');
      loadConnectors();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Auth failed');
    }
  };

  return (
    <div className="connector-manager">
      <div className="cm-header">
        <h2>Connectors</h2>
        <div className="cm-header-actions">
          <button className="cm-btn cm-btn-primary" onClick={() => { setCreating(true); setEditing(null); }}>
            Add Connector
          </button>
          <button className="cm-btn" onClick={onClose}>Back to Search</button>
        </div>
      </div>

      {error && <div className="error">{error}</div>}
      {authStatus && <div className="sync-report">{authStatus}</div>}
      {syncReport && (
        <div className="sync-report">
          Synced {syncReport.docs_processed} documents from {syncReport.connector_name}
          {syncReport.errors > 0 && ` (${syncReport.errors} errors)`}
        </div>
      )}

      {authConnectorId && (
        <div className="cm-form">
          <h3>Telegram Authentication</h3>
          <p className="cm-form-hint" style={{ marginBottom: '0.75rem' }}>
            Enter the code sent to your Telegram app.
          </p>
          <div className="cm-form-row">
            <label>Code</label>
            <input
              type="text"
              value={authCode}
              onChange={(e) => setAuthCode(e.target.value)}
              placeholder="12345"
              autoFocus
            />
          </div>
          <div className="cm-form-row">
            <label>2FA Password (if enabled)</label>
            <input
              type="password"
              value={authPassword}
              onChange={(e) => setAuthPassword(e.target.value)}
              placeholder="Optional"
            />
          </div>
          <div className="cm-form-actions">
            <button className="cm-btn cm-btn-primary" onClick={handleAuthCode}>
              Submit Code
            </button>
            <button className="cm-btn" onClick={() => setAuthConnectorId('')}>
              Cancel
            </button>
          </div>
        </div>
      )}

      {creating && (
        <ConnectorForm
          onSubmit={handleCreate}
          onCancel={() => setCreating(false)}
        />
      )}

      {editing && (
        <ConnectorForm
          initial={editing}
          onSubmit={(req) => handleUpdate(editing.id, req)}
          onCancel={() => setEditing(null)}
        />
      )}

      <div className="cm-list">
        {connectors.length === 0 && !creating && (
          <div className="cm-empty">No connectors configured. Add one to get started.</div>
        )}
        {connectors.map((conn) => (
          <div key={conn.id} className="cm-card">
            <div className="cm-card-header">
              <div>
                <span className="cm-card-name">{conn.name}</span>
                <span className="cm-card-type">{conn.type}</span>
                <span className={`cm-card-status cm-status-${conn.status}`}>{conn.status}</span>
                {!conn.enabled && <span className="cm-card-status cm-status-disabled">disabled</span>}
              </div>
              <div className="cm-card-actions">
                {conn.type === 'telegram' && (
                  <button
                    className="cm-btn cm-btn-sm cm-btn-primary"
                    onClick={() => handleAuthStart(conn.id)}
                  >
                    Connect
                  </button>
                )}
                <button
                  className="cm-btn cm-btn-sm"
                  onClick={() => handleSync(conn.name)}
                  disabled={syncing === conn.name || !conn.enabled}
                >
                  {syncing === conn.name ? 'Syncing...' : 'Sync'}
                </button>
                <button
                  className="cm-btn cm-btn-sm"
                  onClick={() => { setEditing(conn); setCreating(false); }}
                >
                  Edit
                </button>
                <button
                  className="cm-btn cm-btn-sm cm-btn-danger"
                  onClick={() => handleDelete(conn.id)}
                >
                  Delete
                </button>
              </div>
            </div>
            <div className="cm-card-config">
              {Object.entries(conn.config).map(([k, v]) => (
                <span key={k} className="cm-config-item">{k}: {String(v)}</span>
              ))}
              {conn.schedule && (
                <span className="cm-config-item cm-schedule">schedule: {conn.schedule}</span>
              )}
              {conn.last_run && (
                <span className="cm-config-item">last run: {new Date(conn.last_run).toLocaleString()}</span>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

interface FormProps {
  initial?: ConnectorConfig;
  onSubmit: (req: CreateConnectorRequest) => void;
  onCancel: () => void;
}

function ConnectorForm({ initial, onSubmit, onCancel }: FormProps) {
  const [type, setType] = useState(initial?.type || 'filesystem');
  const [name, setName] = useState(initial?.name || '');
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [schedule, setSchedule] = useState(initial?.schedule || '');
  const [configValues, setConfigValues] = useState<Record<string, string>>(() => {
    if (initial?.config) {
      const vals: Record<string, string> = {};
      for (const [k, v] of Object.entries(initial.config)) {
        vals[k] = String(v);
      }
      return vals;
    }
    return {};
  });

  const fields = CONFIG_FIELDS[type] || [];

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const config: Record<string, unknown> = {};
    for (const field of fields) {
      if (configValues[field.key]) {
        config[field.key] = configValues[field.key];
      }
    }
    onSubmit({ type, name, config, enabled, schedule });
  };

  return (
    <form className="cm-form" onSubmit={handleSubmit}>
      <h3>{initial ? 'Edit Connector' : 'New Connector'}</h3>
      <div className="cm-form-row">
        <label>Type</label>
        <select value={type} onChange={(e) => setType(e.target.value)} disabled={!!initial}>
          {CONNECTOR_TYPES.map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
      </div>
      <div className="cm-form-row">
        <label>Name</label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="my-connector"
          required
        />
      </div>
      {fields.map((field) => (
        <div key={field.key} className="cm-form-row">
          <label>{field.label}</label>
          <input
            type="text"
            value={configValues[field.key] || ''}
            onChange={(e) => setConfigValues({ ...configValues, [field.key]: e.target.value })}
            placeholder={field.placeholder}
          />
        </div>
      ))}
      <div className="cm-form-row">
        <label>Schedule (cron)</label>
        <input
          type="text"
          value={schedule}
          onChange={(e) => setSchedule(e.target.value)}
          placeholder="*/30 * * * *"
        />
        <span className="cm-form-hint">Leave empty for manual sync only. Examples: */5 * * * * (every 5 min), 0 * * * * (hourly)</span>
      </div>
      <div className="cm-form-row">
        <label>
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          {' '}Enabled
        </label>
      </div>
      <div className="cm-form-actions">
        <button type="submit" className="cm-btn cm-btn-primary">
          {initial ? 'Update' : 'Create'}
        </button>
        <button type="button" className="cm-btn" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}
