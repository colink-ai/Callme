import { create } from 'zustand';
import { api, apiErrorMessage } from '../api/client';
import type { Domain } from '../types';

const DOMAIN_KEY = 'callme_domain_id';
const DEFAULT_DOMAIN_ID = 'default';

interface DomainState {
  domains: Domain[];
  selectedDomainId: string;
  loading: boolean;
  error: string | null;
  loadDomains: () => Promise<void>;
  setSelectedDomainId: (id: string) => void;
  reset: () => void;
}

function storedDomainID(): string {
  return localStorage.getItem(DOMAIN_KEY) || DEFAULT_DOMAIN_ID;
}

function persistDomainID(id: string) {
  localStorage.setItem(DOMAIN_KEY, id || DEFAULT_DOMAIN_ID);
}

export const useDomainStore = create<DomainState>((set, get) => ({
  domains: [],
  selectedDomainId: storedDomainID(),
  loading: false,
  error: null,

  loadDomains: async () => {
    if (get().loading) return;
    set({ loading: true, error: null });
    try {
      const items = await api.listDomains();
      const current = get().selectedDomainId || storedDomainID();
      const next = items.some((item) => item.id === current)
        ? current
        : items[0]?.id || DEFAULT_DOMAIN_ID;
      persistDomainID(next);
      set({ domains: items, selectedDomainId: next });
    } catch (err) {
      set({ error: apiErrorMessage(err), domains: [] });
    } finally {
      set({ loading: false });
    }
  },

  setSelectedDomainId: (id) => {
    const next = id || DEFAULT_DOMAIN_ID;
    persistDomainID(next);
    set({ selectedDomainId: next });
  },

  reset: () => {
    localStorage.removeItem(DOMAIN_KEY);
    set({ domains: [], selectedDomainId: DEFAULT_DOMAIN_ID, loading: false, error: null });
  },
}));
