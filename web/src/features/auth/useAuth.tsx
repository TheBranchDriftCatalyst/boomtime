import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useSyncExternalStore } from "react";
import { api, ApiError } from "@/lib/api";
import { authStore, broadcastLogout } from "@/lib/auth";
import type { Credentials } from "@/types/api";

interface AuthContextValue {
  isLoggedIn: boolean;
  username: string;
  bootstrapping: boolean;
  login: (creds: Credentials) => Promise<void>;
  register: (creds: Credentials) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

// Refresh threshold: refresh when within 5 minutes of expiry.
const REFRESH_MARGIN_MS = 5 * 60_000;
const CHECK_INTERVAL_MS = 60_000;

export function AuthProvider({ children }: { children: ReactNode }) {
  const snapshot = useSyncExternalStore(
    authStore.subscribe,
    authStore.getSnapshot,
  );
  const [bootstrapping, setBootstrapping] = useState(true);
  const intervalRef = useRef<number | null>(null);

  // Bootstrap the session from the refresh_token cookie on load.
  useEffect(() => {
    let cancelled = false;
    api
      .refreshToken()
      .then((r) => {
        if (!cancelled) authStore.update(r);
      })
      .catch(() => {
        /* not authenticated; routes will redirect to /login */
      })
      .finally(() => {
        if (!cancelled) setBootstrapping(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Silent-refresh loop.
  useEffect(() => {
    function tick() {
      if (!authStore.isLoggedIn()) return;
      const now = Date.now();
      if (now > authStore.tokenExpiry() - REFRESH_MARGIN_MS) {
        api
          .refreshToken()
          .then((r) => authStore.update(r))
          .catch(() => {
            authStore.clear();
            broadcastLogout();
          });
      }
    }
    intervalRef.current = window.setInterval(tick, CHECK_INTERVAL_MS);
    return () => {
      if (intervalRef.current) window.clearInterval(intervalRef.current);
    };
  }, []);

  const login = useCallback(async (creds: Credentials) => {
    const r = await api.login(creds);
    authStore.update(r);
  }, []);

  const register = useCallback(async (creds: Credentials) => {
    const r = await api.register(creds);
    authStore.update(r);
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch (e) {
      // Ignore auth errors on logout; still clear locally.
      if (!(e instanceof ApiError)) throw e;
    } finally {
      authStore.clear();
      broadcastLogout();
    }
  }, []);

  return (
    <AuthContext.Provider
      value={{
        isLoggedIn: snapshot.token !== "" && snapshot.tokenExpiry !== "",
        username: snapshot.username,
        bootstrapping,
        login,
        register,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
