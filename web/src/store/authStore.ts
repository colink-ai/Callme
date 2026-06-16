import { create } from 'zustand';
import { api } from '../api/client';
import type { LoginResult, User } from '../types';

const TOKEN_KEY = 'callme_auth_token';
const EXPIRES_KEY = 'callme_auth_expires_at';

interface AuthState {
  token: string | null;
  user: User | null;
  restoring: boolean;
  restore: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  applyLogin: (result: LoginResult) => void;
}

function storedToken(): string | null {
  const token = localStorage.getItem(TOKEN_KEY);
  const expires = localStorage.getItem(EXPIRES_KEY);
  if (!token || !expires || Date.now() > new Date(expires).getTime()) {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(EXPIRES_KEY);
    return null;
  }
  return token;
}

export function getAuthToken(): string | null {
  return useAuthStore.getState().token ?? storedToken();
}

const initialToken = storedToken();

export const useAuthStore = create<AuthState>((set, get) => ({
  token: initialToken,
  user: null,
  restoring: !!initialToken,

  applyLogin: (result) => {
    localStorage.setItem(TOKEN_KEY, result.token);
    localStorage.setItem(EXPIRES_KEY, result.expiresAt);
    set({ token: result.token, user: result.user });
  },

  restore: async () => {
    const token = storedToken();
    if (!token) {
      if (!token) set({ token: null, user: null });
      return;
    }
    set({ token, restoring: true });
    try {
      const { user } = await api.me();
      set({ user });
    } catch {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(EXPIRES_KEY);
      set({ token: null, user: null });
    } finally {
      set({ restoring: false });
    }
  },

  login: async (username, password) => {
    get().applyLogin(await api.login(username, password));
  },

  register: async (username, password) => {
    get().applyLogin(await api.register(username, password));
  },

  logout: async () => {
    try {
      await api.logout();
    } finally {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(EXPIRES_KEY);
      set({ token: null, user: null });
    }
  },
}));
