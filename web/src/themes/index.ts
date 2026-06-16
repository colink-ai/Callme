// 主题体系（移植自 Colink）：6 套主题通过 data-theme 属性切换
import { theme as antdTheme } from 'antd';
import type { ThemeConfig as AntdThemeConfig } from 'antd';

export type ThemeName = 'emerald' | 'blue' | 'purple' | 'pink' | 'dark' | 'cyan';

export interface ThemeMeta {
  name: ThemeName;
  label: string;
  color: string;
  isDark: boolean;
}

// 主题列表（与 Colink themeConfig.ts 对齐）
export const themeList: ThemeMeta[] = [
  { name: 'emerald', label: '翡翠绿', color: '#10b981', isDark: false },
  { name: 'blue', label: '深海蓝', color: '#3b82f6', isDark: false },
  { name: 'purple', label: '优雅紫', color: '#8b5cf6', isDark: false },
  { name: 'pink', label: '樱花粉', color: '#ec4899', isDark: false },
  { name: 'dark', label: '深邃黑', color: '#18181b', isDark: true },
  { name: 'cyan', label: '科技蓝', color: '#0ea5e9', isDark: false },
];

const STORAGE_KEY = 'callme_theme';

export function getStoredTheme(): ThemeName {
  const v = localStorage.getItem(STORAGE_KEY) as ThemeName | null;
  return themeList.some((t) => t.name === v) ? (v as ThemeName) : 'emerald';
}

export function applyTheme(name: ThemeName) {
  document.documentElement.setAttribute('data-theme', name);
  localStorage.setItem(STORAGE_KEY, name);
}

// antd ConfigProvider 主题：主色跟随，深色主题用 darkAlgorithm
export function antdThemeFor(name: ThemeName): AntdThemeConfig {
  const meta = themeList.find((t) => t.name === name) ?? themeList[0];
  return {
    algorithm: meta.isDark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token: {
      colorPrimary: meta.isDark ? '#10b981' : meta.color,
      colorSuccess: '#52c41a',
      colorWarning: '#faad14',
      colorError: '#ef4444',
      colorInfo: '#1890ff',
      borderRadius: 8,
    },
  };
}
