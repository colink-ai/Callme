import { Avatar, Button, Dropdown, Layout, Menu, Space, Spin, Tag, Typography } from 'antd';
import {
  BgColorsOutlined,
  CheckOutlined,
  CommentOutlined,
  DashboardOutlined,
  DesktopOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import Logo from './components/Logo';
import AuthPage from './pages/Auth';
import ChatPage from './pages/Chat';
import DashboardPage from './pages/Dashboard';
import KnowledgePage from './pages/Knowledge';
import MonitorPage from './pages/Monitor';
import TicketsPage from './pages/Tickets';
import SettingsPage from './pages/Settings';
import UsersPage from './pages/Users';
import { themeList } from './themes';
import { useThemeStore } from './store/themeStore';
import { useAuthStore } from './store/authStore';
import { useChatStore } from './store/chatStore';
import { useEffect } from 'react';

const { Header, Content } = Layout;
const { Text } = Typography;

const menuItems = [
  { key: '/chat', icon: <CommentOutlined />, label: '智能问答' },
];

const adminMenuItems = [
  { key: '/knowledge', icon: <FileSearchOutlined />, label: '知识检索' },
  { key: '/dashboard', icon: <DashboardOutlined />, label: '效能看板' },
  { key: '/monitor', icon: <DesktopOutlined />, label: '会话监控' },
  { key: '/tickets', icon: <FileTextOutlined />, label: '升级工单' },
  { key: '/users', icon: <TeamOutlined />, label: '用户管理' },
  { key: '/settings', icon: <SettingOutlined />, label: '设置' },
];

// 主题切换器（参考 Colink：「主题」按钮 + 点击下拉，色块预览 + 勾选）
function ThemeSwitcher() {
  const { theme, setTheme } = useThemeStore();
  const items = themeList.map((t) => ({
    key: t.name,
    label: (
      <div className="theme-menu-item">
        <span className="theme-color-preview" style={{ background: t.color }} />
        <span className="theme-label">{t.label}</span>
        {theme === t.name && <CheckOutlined className="theme-check-icon" />}
      </div>
    ),
    onClick: () => setTheme(t.name),
  }));
  return (
    <Dropdown menu={{ items, selectedKeys: [theme] }} trigger={['click']} placement="bottomRight">
      <Button type="text" className="theme-switcher-btn" icon={<BgColorsOutlined />}>
        主题
      </Button>
    </Dropdown>
  );
}

export default function App() {
  const navigate = useNavigate();
  const location = useLocation();
  const { token, user, restoring, restore, logout } = useAuthStore();
  const resetChat = useChatStore((s) => s.reset);
  const allItems = user?.role === 'admin' ? [...menuItems, ...adminMenuItems] : menuItems;
  const selected = allItems.find((m) => location.pathname.startsWith(m.key))?.key ?? '/chat';

  useEffect(() => {
    restore();
  }, [restore]);

  if (restoring) {
    return (
      <div style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Spin />
      </div>
    );
  }

  if (!token || !user) return <AuthPage />;

  const roleLabel = user.role === 'admin' ? '管理员' : user.role === 'vip' ? 'VIP' : '普通用户';

  return (
    <Layout style={{ height: '100%' }}>
      <Header className="app-header">
        <div className="app-header-left">
          <Logo size={30} />
          <Menu
            mode="horizontal"
            selectedKeys={[selected]}
            items={allItems}
            onClick={(e) => navigate(e.key)}
            style={{ minWidth: 0, flex: 1, borderBottom: 'none', background: 'transparent' }}
          />
        </div>
        <Space size={16} className="app-header-right">
          <ThemeSwitcher />
          <Dropdown
            menu={{
              items: [
                { key: 'role', label: <Text type="secondary">{roleLabel}</Text>, disabled: true },
                { type: 'divider' },
                {
                  key: 'logout',
                  label: '退出登录',
                  onClick: async () => {
                    resetChat();
                    await logout();
                  },
                },
              ],
            }}
          >
            <Button type="text">
              <Space>
                <Avatar size={24} icon={<UserOutlined />} />
                <span>{user.username}</span>
                <Tag color={user.role === 'vip' ? 'gold' : user.role === 'admin' ? 'red' : 'default'}>{roleLabel}</Tag>
              </Space>
            </Button>
          </Dropdown>
        </Space>
      </Header>
      <Content style={{ overflow: 'auto', background: 'var(--gradient-bg)' }}>
        <Routes>
          <Route path="/" element={<Navigate to="/chat" replace />} />
          <Route path="/chat" element={<ChatPage />} />
          {user.role === 'admin' && (
            <>
              <Route path="/knowledge" element={<KnowledgePage />} />
              <Route path="/dashboard" element={<DashboardPage />} />
              <Route path="/monitor" element={<MonitorPage />} />
              <Route path="/tickets" element={<TicketsPage />} />
              <Route path="/users" element={<UsersPage />} />
              <Route path="/settings" element={<SettingsPage />} />
            </>
          )}
          <Route path="*" element={<Navigate to="/chat" replace />} />
        </Routes>
      </Content>
    </Layout>
  );
}
