import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import {
  clearToken,
  getHealth,
  getMe,
  getToken,
  login as apiLogin,
  onUnauthorized,
  register as apiRegister,
  setToken,
  type User,
} from './api';

interface AuthState {
  user: User | null;
  loading: boolean;
  setupRequired: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [setupRequired, setSetupRequired] = useState(false);

  // On mount: check health (for setup_required) and try to fetch /me with stored token
  useEffect(() => {
    let mounted = true;
    (async () => {
      try {
        const health = await getHealth();
        if (mounted) setSetupRequired(Boolean(health.setup_required));
      } catch {
        // Health endpoint failed — treat as no setup required, network issue
      }
      const token = getToken();
      if (token) {
        try {
          const me = await getMe();
          if (mounted) setUser(me);
        } catch {
          // Token expired or invalid — clear it
          clearToken();
        }
      }
      if (mounted) setLoading(false);
    })();
    return () => { mounted = false; };
  }, []);

  // Listen for 401 from API calls and clear local state
  useEffect(() => {
    return onUnauthorized(() => {
      setUser(null);
    });
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const resp = await apiLogin(username, password);
    setToken(resp.token);
    setUser(resp.user);
  }, []);

  const register = useCallback(async (username: string, password: string) => {
    const resp = await apiRegister(username, password);
    setToken(resp.token);
    setUser(resp.user);
    setSetupRequired(false);
  }, []);

  const logout = useCallback(() => {
    clearToken();
    setUser(null);
  }, []);

  return (
    <AuthContext.Provider value={{ user, loading, setupRequired, login, register, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}
