import { useState, useEffect, useRef } from 'react';
import {
  listConnectors,
  createConnector,
  updateConnector,
  deleteConnector,
  triggerSync,
  streamSyncProgress,
  telegramAuthStart,
  telegramAuthCode,
  type ConnectorConfig,
  type CreateConnectorRequest,
  type SyncJob,
} from './api';
import { useAuth } from './AuthContext';

interface Props {
  onClose: () => void;
}

const CONNECTOR_TYPES = ['filesystem', 'paperless', 'telegram', 'imap'];

const COMMON_FIELDS = [
  { key: 'sync_since_days', label: 'Sync History (days)', placeholder: 'e.g., 30 (empty = all history)' },
  { key: 'sync_since', label: 'Sync Since Date', placeholder: 'YYYY-MM-DD (overridden by days if set)' },
];

const CONFIG_FIELDS: Record<string, { key: string; label: string; placeholder: string; inputType?: string }[]> = {
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
  imap: [
    { key: 'server', label: 'IMAP Server', placeholder: 'imap.mail.me.com' },
    { key: 'port', label: 'Port', placeholder: '993' },
    { key: 'username', label: 'Username', placeholder: 'user@icloud.com' },
    { key: 'password', label: 'Password', placeholder: 'app-specific password', inputType: 'password' },
    { key: 'folders', label: 'Folders', placeholder: 'INBOX (comma-separated)' },
    ...COMMON_FIELDS,
  ],
};

export default function ConnectorManager({ onClose }: Props) {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const canModify = (conn: ConnectorConfig) =>
    isAdmin || (!!conn.user_id && conn.user_id === user?.id);
  const [connectors, setConnectors] = useState<ConnectorConfig[]>([]);
  const [editing, setEditing] = useState<ConnectorConfig | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');
  const [syncJobs, setSyncJobs] = useState<Record<string, SyncJob>>({});
  const cleanupRefs = useRef<Record<string, () => void>>({});
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

  const handleSync = async (conn: ConnectorConfig) => {
    setError('');
    try {
      const job = await triggerSync(conn.id);
      setSyncJobs((prev) => ({ ...prev, [conn.name]: job }));

      const cleanup = streamSyncProgress(
        conn.id,
        (update) => setSyncJobs((prev) => ({ ...prev, [conn.name]: update })),
        () => { delete cleanupRefs.current[conn.name]; },
      );
      cleanupRefs.current[conn.name] = cleanup;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sync failed');
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
      {Object.entries(syncJobs).map(([name, job]) => (
        <div key={name} className={`sync-progress ${job.status === 'failed' ? 'sync-progress-error' : ''}`}>
          <div className="sync-progress-header">
            <span className="sync-progress-label">
              {job.status === 'running' ? `Syncing ${name}...` : job.status === 'failed' ? `Failed: ${name}` : `Synced ${name}`}
            </span>
            <span className="sync-progress-stats">
              {job.docs_processed}{job.docs_total > 0 ? `/${job.docs_total}` : ''} docs
              {job.errors > 0 && ` (${job.errors} errors)`}
            </span>
            {job.status !== 'running' && (
              <button className="sync-progress-dismiss" onClick={() => setSyncJobs((prev) => { const n = { ...prev }; delete n[name]; return n; })}>&times;</button>
            )}
          </div>
          {job.status === 'running' && job.docs_total > 0 && (
            <div className="sync-progress-bar-bg">
              <div className="sync-progress-bar-fill" style={{ width: `${Math.round((job.docs_processed / job.docs_total) * 100)}%` }} />
            </div>
          )}
        </div>
      ))}

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
                {conn.shared && <span className="cm-card-status cm-status-shared">shared</span>}
                {!conn.enabled && <span className="cm-card-status cm-status-disabled">disabled</span>}
              </div>
              <div className="cm-card-actions">
                {conn.type === 'telegram' && canModify(conn) && (
                  <button
                    className="cm-btn cm-btn-sm cm-btn-primary"
                    onClick={() => handleAuthStart(conn.id)}
                  >
                    Connect
                  </button>
                )}
                {canModify(conn) && (
                  <button
                    className="cm-btn cm-btn-sm"
                    onClick={() => handleSync(conn)}
                    disabled={syncJobs[conn.name]?.status === 'running' || !conn.enabled}
                  >
                    {syncJobs[conn.name]?.status === 'running' ? 'Syncing...' : 'Sync'}
                  </button>
                )}
                {canModify(conn) && (
                  <button
                    className="cm-btn cm-btn-sm"
                    onClick={() => { setEditing(conn); setCreating(false); }}
                  >
                    Edit
                  </button>
                )}
                {canModify(conn) && (
                  <button
                    className="cm-btn cm-btn-sm cm-btn-danger"
                    onClick={() => handleDelete(conn.id)}
                  >
                    Delete
                  </button>
                )}
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
  const [shared, setShared] = useState(initial?.shared ?? false);
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
    onSubmit({ type, name, config, enabled, schedule, shared });
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
            type={field.inputType || 'text'}
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
      <div className="cm-form-row">
        <label>
          <input
            type="checkbox"
            checked={shared}
            onChange={(e) => setShared(e.target.checked)}
          />
          {' '}Shared
        </label>
        <span className="cm-form-hint">When enabled, all users can search documents from this connector. Otherwise, only you can see them.</span>
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
