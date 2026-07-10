// In-memory access token store (never persisted to localStorage) plus the
// refresh loop and cross-tab logout, ported from hakatime's storage.js/auth.js.
import type { AuthResponse } from "@/types/api";

interface TokenState {
  token: string;
  tokenExpiry: string;
  username: string;
}

let state: TokenState = { token: "", tokenExpiry: "", username: "" };

type Listener = () => void;
const listeners = new Set<Listener>();

function emit() {
  for (const l of listeners) l();
}

export const authStore = {
  subscribe(l: Listener): () => void {
    listeners.add(l);
    return () => listeners.delete(l);
  },
  getSnapshot(): TokenState {
    return state;
  },
  isLoggedIn(): boolean {
    return state.token !== "" && state.tokenExpiry !== "";
  },
  getUsername(): string {
    return state.username;
  },
  /**
   * Value for the Authorization header. The backend already returns the access
   * token as base64(uuid) (matching hakatime), so it is sent AS-IS — do NOT
   * re-encode it, or the header would be double-base64'd and rejected (403).
   */
  authHeader(): string | null {
    if (!state.token) return null;
    return `Basic ${state.token}`;
  },
  update(r: AuthResponse) {
    state = {
      token: r.token,
      tokenExpiry: r.tokenExpiry,
      username: r.tokenUsername,
    };
    emit();
  },
  clear() {
    state = { token: "", tokenExpiry: "", username: "" };
    emit();
  },
  tokenExpiry(): number {
    return state.tokenExpiry ? new Date(state.tokenExpiry).getTime() : 0;
  },
};

// Broadcast logout to other tabs. A storage listener (installed in main.tsx)
// clears the in-memory token when it sees this key change.
export function broadcastLogout() {
  try {
    window.localStorage.setItem("logout", String(Date.now()));
  } catch {
    /* ignore */
  }
}
