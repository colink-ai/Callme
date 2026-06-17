import { Avatar, Dropdown, Layout, Menu, Space, Spin, Tag, Typography } from 'antd';
import {
  BgColorsOutlined,
  BulbOutlined,
  CheckOutlined,
  CommentOutlined,
  DashboardOutlined,
  DesktopOutlined,
  FileTextOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import Logo from './components/Logo';
import AuthPage from './pages/Auth';
import ChatPage from './pages/Chat';
import CurationPage from './pages/Curation';
import DashboardPage from './pages/Dashboard';
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
  { key: '/dashboard', icon: <DashboardOutlined />, label: '效能看板' },
  { key: '/monitor', icon: <DesktopOutlined />, label: '会话监控' },
  { key: '/tickets', icon: <FileTextOutlined />, label: '升级工单' },
  { key: '/users', icon: <TeamOutlined />, label: '用户管理' },
  { key: '/settings', icon: <SettingOutlined />, label: '设置' },
];

const knowledgeMenuItems = [
  { key: '/curation', icon: <BulbOutlined />, label: '知识沉淀' },
];

const roleLabels = {
  normal: '普通用户',
  vip: 'VIP',
  knowledge_expert: '知识专家',
  admin: '管理员',
} as const;

// 主题切换器（参考 ReviewBuddy：只有图标，没有文字）
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
      <BgColorsOutlined style={{ fontSize: 18, cursor: 'pointer', color: 'var(--text-primary)' }} />
    </Dropdown>
  );
}

export default function App() {
  const navigate = useNavigate();
  const location = useLocation();
  const { token, user, version, restoring, restore, logout } = useAuthStore();
  const resetChat = useChatStore((s) => s.reset);
  const roles = user?.roles?.length ? user.roles : user ? [user.role] : [];
  const hasRole = (role: string) => roles.includes(role as typeof roles[number]);
  const canManageKnowledge = hasRole('admin') || hasRole('knowledge_expert');
  const allItems = [
    ...menuItems,
    ...(canManageKnowledge ? knowledgeMenuItems : []),
    ...(hasRole('admin') ? adminMenuItems : []),
  ];
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

  const roleLabel = roles.map((role) => roleLabels[role] ?? role).join(' / ');
  const roleColor = hasRole('admin')
    ? 'red'
    : hasRole('knowledge_expert')
      ? 'cyan'
      : hasRole('vip')
        ? 'gold'
        : 'default';

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
        <Space size={12} className="app-header-right">
          {version ? <Tag style={{ margin: 0, fontSize: 11 }}>v{version}</Tag> : null}
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
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, cursor: 'pointer', color: 'var(--text-primary)' }}>
              <Avatar size={22} icon={<UserOutlined />} />
              <span style={{ fontSize: 14 }}>{user.username}</span>
              <Tag color={roleColor} style={{ margin: 0, fontSize: 11 }}>{roleLabel}</Tag>
            </span>
          </Dropdown>
          <ThemeSwitcher />
        </Space>
      </Header>
      <Content style={{ overflow: 'auto', background: 'var(--gradient-bg)' }}>
        <Routes>
          <Route path="/" element={<Navigate to="/chat" replace />} />
          <Route path="/chat" element={<ChatPage />} />
          {canManageKnowledge && <Route path="/curation" element={<CurationPage />} />}
          {hasRole('admin') && (
            <>
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
