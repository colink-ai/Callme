import { Avatar, Dropdown, Layout, Menu, Space, Spin, Tag, Typography } from 'antd';
import {
  BgColorsOutlined,
  BulbOutlined,
  CheckOutlined,
  CommentOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  DesktopOutlined,
  FileTextOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import Logo from './components/Logo';
import AITaskPanel from './components/AITaskPanel';
import AuthPage from './pages/Auth';
import ChatPage from './pages/Chat';
import CurationPage from './pages/Curation';
import DashboardPage from './pages/Dashboard';
import DomainsPage from './pages/Domains';
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
  { key: '/domains', icon: <DatabaseOutlined />, label: '领域管理' },
  { key: '/settings', icon: <SettingOutlined />, label: '设置' },
];

const knowledgeMenuItems = [
  { key: '/curation', icon: <BulbOutlined />, label: '知识沉淀' },
];

const roleLabels = {
  normal: '普通用户',
  vip: 'VIP',
  knowledge_staff: '知识专员',
  knowledge_expert: '知识专家',
  admin: '管理员',
} as const;

const roleColors = {
  normal: 'default',
  vip: 'gold',
  knowledge_staff: 'blue',
  knowledge_expert: 'cyan',
  admin: 'red',
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
  const { token, user, activeRole, version, restoring, restore, logout, setActiveRole } = useAuthStore();
  const resetChat = useChatStore((s) => s.reset);
  const roles = user?.roles?.length ? user.roles : user ? [user.role] : [];
  const usingRole = activeRole && roles.includes(activeRole as typeof roles[number]) ? activeRole : user?.role;
  const hasActiveRole = (role: string) => usingRole === role;
  const canManageKnowledge = hasActiveRole('admin') || hasActiveRole('knowledge_expert') || hasActiveRole('knowledge_staff');
  const canUseInternalAITasks = canManageKnowledge;
  const allItems = [
    ...menuItems,
    ...(canManageKnowledge ? knowledgeMenuItems : []),
    ...(hasActiveRole('admin') ? adminMenuItems : []),
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

  const roleLabel = roleLabels[usingRole as keyof typeof roleLabels] ?? usingRole ?? '普通用户';
  const roleColor = roleColors[usingRole as keyof typeof roleColors] ?? 'default';
  const switchRole = (role: string) => {
    setActiveRole(role);
    const nextIsAdmin = role === 'admin';
    const nextCanManageKnowledge = nextIsAdmin || role === 'knowledge_expert' || role === 'knowledge_staff';
    const onAdminOnlyPage = ['/dashboard', '/monitor', '/tickets', '/users', '/settings'].some((path) => location.pathname.startsWith(path));
    if ((onAdminOnlyPage && !nextIsAdmin) || (location.pathname.startsWith('/curation') && !nextCanManageKnowledge)) {
      navigate('/chat');
    }
  };

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
                { key: 'role-title', label: <Text type="secondary">当前角色</Text>, disabled: true },
                ...roles.map((role) => ({
                  key: `role-${role}`,
                  label: (
                    <Space>
                      <Tag color={roleColors[role as keyof typeof roleColors] ?? 'default'} style={{ margin: 0 }}>
                        {roleLabels[role as keyof typeof roleLabels] ?? role}
                      </Tag>
                      {role === usingRole && <CheckOutlined />}
                    </Space>
                  ),
                  onClick: () => switchRole(role),
                })),
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
          {hasActiveRole('admin') && (
            <>
              <Route path="/dashboard" element={<DashboardPage />} />
              <Route path="/monitor" element={<MonitorPage />} />
              <Route path="/tickets" element={<TicketsPage />} />
              <Route path="/users" element={<UsersPage />} />
              <Route path="/domains" element={<DomainsPage />} />
              <Route path="/settings" element={<SettingsPage />} />
            </>
          )}
          <Route path="*" element={<Navigate to="/chat" replace />} />
        </Routes>
      </Content>
      {canUseInternalAITasks && <AITaskPanel />}
    </Layout>
  );
}
