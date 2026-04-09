import { useState, useEffect } from 'react';
import {
  getEmbeddingSettings, updateEmbeddingSettings,
  getRerankSettings, updateRerankSettings,
  type EmbeddingSettings, type RerankSettings,
} from './api';

interface Props {
  onClose: () => void;
}

const EMBEDDING_PROVIDERS = [
  { value: '', label: 'Disabled (BM25 only)' },
  { value: 'ollama', label: 'Ollama (self-hosted)' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'voyage', label: 'Voyage AI' },
  { value: 'cohere', label: 'Cohere' },
];

const DEFAULT_EMBEDDING_MODELS: Record<string, string> = {
  ollama: 'nomic-embed-text',
  openai: 'text-embedding-3-small',
  voyage: 'voyage-3-large',
  cohere: 'embed-v4.0',
};

const RERANK_PROVIDERS = [
  { value: '', label: 'Disabled' },
  { value: 'voyage', label: 'Voyage AI' },
  { value: 'cohere', label: 'Cohere' },
];

const DEFAULT_RERANK_MODELS: Record<string, string> = {
  voyage: 'rerank-2',
  cohere: 'rerank-v3.5',
};

export default function Settings({ onClose }: Props) {
  const [embSettings, setEmbSettings] = useState<EmbeddingSettings>({
    provider: '', model: '', api_key: '', ollama_url: 'http://localhost:11434',
  });
  const [rerankSettings, setRerankSettings] = useState<RerankSettings>({
    provider: '', model: '', api_key: '',
  });
  const [saving, setSaving] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    getEmbeddingSettings().then(setEmbSettings).catch((err) => setError(err.message));
    getRerankSettings().then(setRerankSettings).catch(() => {});
  }, []);

  const handleEmbProviderChange = (provider: string) => {
    setEmbSettings({ ...embSettings, provider, model: DEFAULT_EMBEDDING_MODELS[provider] || '' });
  };

  const handleRerankProviderChange = (provider: string) => {
    setRerankSettings({ ...rerankSettings, provider, model: DEFAULT_RERANK_MODELS[provider] || '' });
  };

  const handleSaveEmbedding = async () => {
    setSaving('embedding');
    setError('');
    setSuccess('');
    try {
      const updated = await updateEmbeddingSettings(embSettings);
      setEmbSettings(updated);
      setSuccess('Embedding settings saved');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving('');
    }
  };

  const handleSaveRerank = async () => {
    setSaving('rerank');
    setError('');
    setSuccess('');
    try {
      const updated = await updateRerankSettings(rerankSettings);
      setRerankSettings(updated);
      setSuccess('Rerank settings saved');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving('');
    }
  };

  const embNeedsAPIKey = ['openai', 'voyage', 'cohere'].includes(embSettings.provider);
  const embNeedsOllamaURL = embSettings.provider === 'ollama';
  const rerankNeedsAPIKey = ['voyage', 'cohere'].includes(rerankSettings.provider);

  return (
    <div className="settings">
      <div className="cm-header">
        <h2>Settings</h2>
        <button className="cm-btn" onClick={onClose}>Back to Search</button>
      </div>

      {error && <div className="error">{error}</div>}
      {success && <div className="sync-progress">{success}</div>}

      <div className="cm-form">
        <h3>Embedding Provider</h3>
        <p className="cm-form-hint" style={{ marginBottom: '1rem' }}>
          Configure semantic search. When enabled, documents are embedded and search uses hybrid BM25 + vector ranking.
        </p>

        <div className="cm-form-row">
          <label>Provider</label>
          <select value={embSettings.provider} onChange={(e) => handleEmbProviderChange(e.target.value)}>
            {EMBEDDING_PROVIDERS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
        </div>

        {embSettings.provider && (
          <div className="cm-form-row">
            <label>Model</label>
            <input type="text" value={embSettings.model}
              onChange={(e) => setEmbSettings({ ...embSettings, model: e.target.value })}
              placeholder={DEFAULT_EMBEDDING_MODELS[embSettings.provider] || 'model name'} />
          </div>
        )}

        {embNeedsOllamaURL && (
          <div className="cm-form-row">
            <label>Ollama URL</label>
            <input type="text" value={embSettings.ollama_url}
              onChange={(e) => setEmbSettings({ ...embSettings, ollama_url: e.target.value })}
              placeholder="http://localhost:11434" />
          </div>
        )}

        {embNeedsAPIKey && (
          <div className="cm-form-row">
            <label>API Key</label>
            <input type="password" value={embSettings.api_key}
              onChange={(e) => setEmbSettings({ ...embSettings, api_key: e.target.value })}
              placeholder="sk-..." />
            <span className="cm-form-hint">Your key is encrypted at rest and never exposed in full via the API.</span>
          </div>
        )}

        <div className="cm-form-actions">
          <button className="cm-btn cm-btn-primary" onClick={handleSaveEmbedding} disabled={saving === 'embedding'}>
            {saving === 'embedding' ? 'Saving...' : 'Save Embedding Settings'}
          </button>
        </div>
      </div>

      <div className="cm-form" style={{ marginTop: '1.5rem' }}>
        <h3>Reranking Provider</h3>
        <p className="cm-form-hint" style={{ marginBottom: '1rem' }}>
          Second-pass reranking improves search relevance by scoring each result with a cross-encoder model. Especially helpful for multilingual search.
        </p>

        <div className="cm-form-row">
          <label>Provider</label>
          <select value={rerankSettings.provider} onChange={(e) => handleRerankProviderChange(e.target.value)}>
            {RERANK_PROVIDERS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
        </div>

        {rerankSettings.provider && (
          <div className="cm-form-row">
            <label>Model</label>
            <input type="text" value={rerankSettings.model}
              onChange={(e) => setRerankSettings({ ...rerankSettings, model: e.target.value })}
              placeholder={DEFAULT_RERANK_MODELS[rerankSettings.provider] || 'model name'} />
          </div>
        )}

        {rerankNeedsAPIKey && (
          <div className="cm-form-row">
            <label>API Key</label>
            <input type="password" value={rerankSettings.api_key}
              onChange={(e) => setRerankSettings({ ...rerankSettings, api_key: e.target.value })}
              placeholder="API key (or leave empty to reuse embedding key)" />
            <span className="cm-form-hint">Leave empty to reuse the embedding API key (if same provider).</span>
          </div>
        )}

        <div className="cm-form-actions">
          <button className="cm-btn cm-btn-primary" onClick={handleSaveRerank} disabled={saving === 'rerank'}>
            {saving === 'rerank' ? 'Saving...' : 'Save Rerank Settings'}
          </button>
        </div>
      </div>
    </div>
  );
}
