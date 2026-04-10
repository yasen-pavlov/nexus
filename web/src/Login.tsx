import { useState, type FormEvent } from 'react';
import { useAuth } from './AuthContext';

export default function Login() {
  const { login, register, setupRequired } = useAuth();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const isRegister = setupRequired;

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    if (!username.trim() || !password) {
      setError('Username and password are required');
      return;
    }
    if (isRegister && password.length < 8) {
      setError('Password must be at least 8 characters');
      return;
    }
    setSubmitting(true);
    try {
      if (isRegister) {
        await register(username.trim(), password);
      } else {
        await login(username.trim(), password);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Authentication failed');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="login-page">
      <div className="login-card">
        <h1 className="login-title">Nexus</h1>
        <p className="login-subtitle">
          {isRegister ? 'Create the first admin account' : 'Sign in to continue'}
        </p>
        {isRegister && (
          <div className="login-info">
            No users exist yet. The first registered account will become the admin.
          </div>
        )}
        <form onSubmit={handleSubmit} className="login-form">
          <label className="login-label">
            Username
            <input
              type="text"
              className="login-input"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              autoFocus
              disabled={submitting}
            />
          </label>
          <label className="login-label">
            Password
            <input
              type="password"
              className="login-input"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete={isRegister ? 'new-password' : 'current-password'}
              disabled={submitting}
            />
          </label>
          {error && <div className="login-error">{error}</div>}
          <button type="submit" className="login-submit" disabled={submitting}>
            {submitting ? 'Please wait...' : isRegister ? 'Create admin account' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  );
}
