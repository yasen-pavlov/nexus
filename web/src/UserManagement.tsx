import { useEffect, useState, type FormEvent } from 'react';
import { changePassword, createUser, deleteUser, listUsers, type User } from './api';
import { useAuth } from './AuthContext';

interface Props {
  onClose: () => void;
}

export default function UserManagement({ onClose }: Props) {
  const { user: currentUser } = useAuth();
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  // Create user form
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newRole, setNewRole] = useState<'admin' | 'user'>('user');
  const [creating, setCreating] = useState(false);

  // Change password modal
  const [pwTarget, setPwTarget] = useState<User | null>(null);
  const [pwValue, setPwValue] = useState('');
  const [pwSubmitting, setPwSubmitting] = useState(false);

  const reload = () => {
    setLoading(true);
    setError('');
    listUsers()
      .then(setUsers)
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load users'))
      .finally(() => setLoading(false));
  };

  useEffect(reload, []);

  const handleCreate = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    if (!newUsername.trim() || newPassword.length < 8) {
      setError('Username required and password must be at least 8 characters');
      return;
    }
    setCreating(true);
    try {
      await createUser(newUsername.trim(), newPassword, newRole);
      setNewUsername('');
      setNewPassword('');
      setNewRole('user');
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Create failed');
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (u: User) => {
    if (!confirm(`Delete user "${u.username}"? This cannot be undone.`)) return;
    setError('');
    try {
      await deleteUser(u.id);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed');
    }
  };

  const openChangePassword = (u: User) => {
    setPwTarget(u);
    setPwValue('');
  };

  const handleChangePassword = async (e: FormEvent) => {
    e.preventDefault();
    if (!pwTarget) return;
    if (pwValue.length < 8) {
      setError('Password must be at least 8 characters');
      return;
    }
    setPwSubmitting(true);
    setError('');
    try {
      await changePassword(pwTarget.id, pwValue);
      setPwTarget(null);
      setPwValue('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Change password failed');
    } finally {
      setPwSubmitting(false);
    }
  };

  return (
    <div className="cm">
      <div className="cm-header">
        <h2>User management</h2>
        <button className="sync-button" onClick={onClose}>Close</button>
      </div>

      {error && <div className="error">{error}</div>}

      <section className="cm-section">
        <h3>Create user</h3>
        <form className="cm-form" onSubmit={handleCreate}>
          <input
            type="text"
            placeholder="Username"
            value={newUsername}
            onChange={(e) => setNewUsername(e.target.value)}
            disabled={creating}
            className="cm-input"
          />
          <input
            type="password"
            placeholder="Password (min 8 chars)"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            disabled={creating}
            className="cm-input"
          />
          <select
            value={newRole}
            onChange={(e) => setNewRole(e.target.value as 'admin' | 'user')}
            disabled={creating}
            className="cm-input"
          >
            <option value="user">user</option>
            <option value="admin">admin</option>
          </select>
          <button type="submit" className="sync-button" disabled={creating}>
            {creating ? 'Creating...' : 'Create'}
          </button>
        </form>
      </section>

      <section className="cm-section">
        <h3>Users</h3>
        {loading ? (
          <div className="loading">Loading...</div>
        ) : (
          <table className="cm-table">
            <thead>
              <tr>
                <th>Username</th>
                <th>Role</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td>{u.username}{currentUser?.id === u.id && ' (you)'}</td>
                  <td>{u.role}</td>
                  <td>
                    <button className="sync-button" onClick={() => openChangePassword(u)}>
                      Change password
                    </button>
                    {currentUser?.id !== u.id && (
                      <button className="sync-button cm-btn-danger" onClick={() => handleDelete(u)}>
                        Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {pwTarget && (
        <div className="cm-modal-overlay" onClick={() => setPwTarget(null)}>
          <div className="cm-modal" onClick={(e) => e.stopPropagation()}>
            <h3>Change password for {pwTarget.username}</h3>
            <form onSubmit={handleChangePassword}>
              <input
                type="password"
                placeholder="New password (min 8 chars)"
                value={pwValue}
                onChange={(e) => setPwValue(e.target.value)}
                disabled={pwSubmitting}
                className="cm-input"
                autoFocus
              />
              <div className="cm-modal-actions">
                <button type="button" className="sync-button" onClick={() => setPwTarget(null)} disabled={pwSubmitting}>
                  Cancel
                </button>
                <button type="submit" className="sync-button" disabled={pwSubmitting}>
                  {pwSubmitting ? 'Saving...' : 'Save'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
