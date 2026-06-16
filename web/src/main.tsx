import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import App from './App';
import { antdThemeFor } from './themes';
import { useThemeStore } from './store/themeStore';
import './index.css';

// antd 主题跟随当前主题（Colink 六套主题，深邃黑用 darkAlgorithm）
function Root() {
  const theme = useThemeStore((s) => s.theme);
  return (
    <ConfigProvider locale={zhCN} theme={antdThemeFor(theme)}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <Root />
  </React.StrictMode>,
);
