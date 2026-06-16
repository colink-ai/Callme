// 主题状态：持久化在 localStorage，切换时更新 data-theme + antd token
import { create } from 'zustand';
import { applyTheme, getStoredTheme, type ThemeName } from '../themes';

interface ThemeState {
  theme: ThemeName;
  setTheme: (name: ThemeName) => void;
}

const initial = getStoredTheme();
applyTheme(initial);

export const useThemeStore = create<ThemeState>((set) => ({
  theme: initial,
  setTheme: (name) => {
    applyTheme(name);
    set({ theme: name });
  },
}));
