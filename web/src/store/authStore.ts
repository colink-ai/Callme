import { create } from 'zustand';
import { api } from '../api/client';
import type { LoginResult, User } from '../types';

const TOKEN_KEY = 'callme_auth_token';
const EXPIRES_KEY = 'callme_auth_expires_at';
const ACTIVE_ROLE_KEY = 'callme_active_role';

interface AuthState {
  token: string | null;
  user: User | null;
  activeRole: string;
  version: string;
  restoring: boolean;
  restore: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  applyLogin: (result: LoginResult) => void;
  setActiveRole: (role: string) => void;
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
  activeRole: localStorage.getItem(ACTIVE_ROLE_KEY) || '',
  version: '',
  restoring: !!initialToken,

  applyLogin: (result) => {
    localStorage.setItem(TOKEN_KEY, result.token);
    localStorage.setItem(EXPIRES_KEY, result.expiresAt);
    const roles = result.user.roles?.length ? result.user.roles : [result.user.role];
    const storedRole = localStorage.getItem(ACTIVE_ROLE_KEY);
    const activeRole = storedRole && roles.includes(storedRole as typeof roles[number]) ? storedRole : result.user.role;
    localStorage.setItem(ACTIVE_ROLE_KEY, activeRole);
    set({ token: result.token, user: result.user, activeRole });
  },

  restore: async () => {
    const token = storedToken();
    if (!token) {
      if (!token) set({ token: null, user: null, version: '' });
      return;
    }
    set({ token, restoring: true });
    try {
      const { user, version } = await api.me();
      const roles = user.roles?.length ? user.roles : [user.role];
      const storedRole = localStorage.getItem(ACTIVE_ROLE_KEY);
      const activeRole = storedRole && roles.includes(storedRole as typeof roles[number]) ? storedRole : user.role;
      localStorage.setItem(ACTIVE_ROLE_KEY, activeRole);
      set({ user, version: version || '', activeRole });
    } catch {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(EXPIRES_KEY);
      localStorage.removeItem(ACTIVE_ROLE_KEY);
      set({ token: null, user: null, version: '', activeRole: '' });
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
      localStorage.removeItem(ACTIVE_ROLE_KEY);
      set({ token: null, user: null, activeRole: '' });
    }
  },

  setActiveRole: (role) => {
    const user = get().user;
    const roles = user?.roles?.length ? user.roles : user ? [user.role] : [];
    if (!roles.includes(role as typeof roles[number])) return;
    localStorage.setItem(ACTIVE_ROLE_KEY, role);
    set({ activeRole: role });
  },
}));
