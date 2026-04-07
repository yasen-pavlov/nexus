import { useState, useEffect } from 'react';
import { getEmbeddingSettings, updateEmbeddingSettings, type EmbeddingSettings } from './api';

interface Props {
  onClose: () => void;
}

const PROVIDERS = [
  { value: '', label: 'Disabled (BM25 only)' },
  { value: 'ollama', label: 'Ollama (self-hosted)' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'voyage', label: 'Voyage AI' },
  { value: 'cohere', label: 'Cohere' },
];

const DEFAULT_MODELS: Record<string, string> = {
  ollama: 'nomic-embed-text',
  openai: 'text-embedding-3-small',
  voyage: 'voyage-3-large',
  cohere: 'embed-v4.0',
};

export default function Settings({ onClose }: Props) {
  const [settings, setSettings] = useState<EmbeddingSettings>({
    provider: '',
    model: '',
    api_key: '',
    ollama_url: 'http://localhost:11434',
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    getEmbeddingSettings()
      .then(setSettings)
      .catch((err) => setError(err.message));
  }, []);

  const handleProviderChange = (provider: string) => {
    setSettings({
      ...settings,
      provider,
      model: DEFAULT_MODELS[provider] || '',
    });
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');
    setSuccess('');
    try {
      const updated = await updateEmbeddingSettings(settings);
      setSettings(updated);
      setSuccess('Settings saved successfully');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  };

  const needsAPIKey = ['openai', 'voyage', 'cohere'].includes(settings.provider);
  const needsOllamaURL = settings.provider === 'ollama';

  return (
    <div className="settings">
      <div className="cm-header">
        <h2>Settings</h2>
        <button className="cm-btn" onClick={onClose}>Back to Search</button>
      </div>

      <div className="cm-form">
        <h3>Embedding Provider</h3>
        <p className="cm-form-hint" style={{ marginBottom: '1rem' }}>
          Configure semantic search. When enabled, documents are embedded and search uses hybrid BM25 + vector ranking.
        </p>

        <div className="cm-form-row">
          <label>Provider</label>
          <select
            value={settings.provider}
            onChange={(e) => handleProviderChange(e.target.value)}
          >
            {PROVIDERS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
        </div>

        {settings.provider && (
          <div className="cm-form-row">
            <label>Model</label>
            <input
              type="text"
              value={settings.model}
              onChange={(e) => setSettings({ ...settings, model: e.target.value })}
              placeholder={DEFAULT_MODELS[settings.provider] || 'model name'}
            />
          </div>
        )}

        {needsOllamaURL && (
          <div className="cm-form-row">
            <label>Ollama URL</label>
            <input
              type="text"
              value={settings.ollama_url}
              onChange={(e) => setSettings({ ...settings, ollama_url: e.target.value })}
              placeholder="http://localhost:11434"
            />
          </div>
        )}

        {needsAPIKey && (
          <div className="cm-form-row">
            <label>API Key</label>
            <input
              type="password"
              value={settings.api_key}
              onChange={(e) => setSettings({ ...settings, api_key: e.target.value })}
              placeholder="sk-..."
            />
            <span className="cm-form-hint">Your key is encrypted at rest and never exposed in full via the API.</span>
          </div>
        )}

        {error && <div className="error">{error}</div>}
        {success && <div className="sync-report">{success}</div>}

        <div className="cm-form-actions">
          <button
            className="cm-btn cm-btn-primary"
            onClick={handleSave}
            disabled={saving}
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}
