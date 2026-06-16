// 后端 API 客户端
import axios from 'axios';
import type {
  AgentSettings,
  DailyPoint,
  HotQuestion,
  KnowledgeSourceInfo,
  LoginResult,
  Message,
  PoolSettings,
  Session,
  SessionView,
  StatsOverview,
  Ticket,
  User,
  UserRole,
} from '../types';

const http = axios.create({ baseURL: '/api/v1', timeout: 30000 });

http.interceptors.request.use((config) => {
  const token = localStorage.getItem('callme_auth_token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

export const api = {
  // 认证
  register: (username: string, password: string) =>
    http.post<LoginResult>('/auth/register', { username, password }).then((r) => r.data),
  login: (username: string, password: string) =>
    http.post<LoginResult>('/auth/login', { username, password }).then((r) => r.data),
  logout: () => http.post('/auth/logout'),
  me: () => http.get<{ user: User }>('/auth/me').then((r) => r.data),
  listUsers: () => http.get<{ users: User[] | null }>('/users').then((r) => r.data.users ?? []),
  updateUserRole: (id: string, role: UserRole) => http.put(`/users/${id}/role`, { role }),
  deleteUser: (id: string) => http.delete(`/users/${id}`),

  // 会话
  createSession: () =>
    http.post<SessionView>('/sessions', undefined, { timeout: 90000 }).then((r) => r.data),
  getCurrentSession: () =>
    http
      .get<{ session: SessionView | null }>('/sessions/current')
      .then((r) => r.data.session),
  listMySessions: () =>
    http.get<{ sessions: Session[] | null }>('/sessions/history').then((r) => r.data.sessions ?? []),
  getSession: (id: string) => http.get<Session>(`/sessions/${id}`).then((r) => r.data),
  listMessages: (id: string) =>
    http.get<{ messages: Message[] | null }>(`/sessions/${id}/messages`).then((r) => r.data.messages ?? []),
  closeSession: (id: string, byAdmin = false) =>
    http.delete(`/sessions/${id}`, { params: byAdmin ? { by: 'admin' } : {} }),
  deleteSessionHistory: (id: string) => http.delete(`/sessions/${id}/history`),
  continueSession: (id: string) =>
    http.post<SessionView>(`/sessions/${id}/continue`, undefined, { timeout: 90000 }).then((r) => r.data),
  listLiveSessions: (includeClosed = false) =>
    http
      .get<{ active: SessionView[] | null; queued: SessionView[] | null; closed?: Session[] }>(
        '/sessions',
        { params: includeClosed ? { include: 'closed' } : {} },
      )
      .then((r) => ({
        active: r.data.active ?? [],
        queued: r.data.queued ?? [],
        closed: r.data.closed ?? [],
      })),

  // 反馈
  submitFeedback: (payload: {
    sessionId: string;
    messageId: string;
    rating: 'up' | 'down';
    correction?: string;
  }) => http.post('/feedback', payload),
  getLearningNotes: () =>
    http.get<{ notes: string }>('/learning/notes').then((r) => r.data.notes),

  // 工单
  createHandoff: (sessionId: string, reason: string) =>
    http.post<Ticket>(`/sessions/${sessionId}/handoff`, { reason }).then((r) => r.data),
  listTickets: () =>
    http.get<{ tickets: Ticket[] | null }>('/tickets').then((r) => r.data.tickets ?? []),

  // 设置
  getAgentSettings: () => http.get<AgentSettings>('/settings/agent').then((r) => r.data),
  updateAgentSettings: (s: AgentSettings) =>
    http.put<AgentSettings>('/settings/agent', s).then((r) => r.data),
  getPoolSettings: () => http.get<PoolSettings>('/settings/pool').then((r) => r.data),
  updatePoolSettings: (s: PoolSettings) =>
    http.put<PoolSettings>('/settings/pool', s).then((r) => r.data),
  getAgentTypes: () =>
    http
      .get<{ types: { type: string; name: string; description: string; defaultPath?: string }[] }>('/agent/types')
      .then((r) => r.data.types),
  checkAgentHealth: () =>
    http.post<{ healthy: boolean; error?: string }>('/agent/health').then((r) => r.data),

  // 知识源
  listKnowledgeSources: () =>
    http
      .get<{ sources: KnowledgeSourceInfo[] | null }>('/knowledge/sources')
      .then((r) => r.data.sources ?? []),
  checkKnowledgeHealth: () =>
    http
      .post<{ sources: KnowledgeSourceInfo[] | null }>('/knowledge/health')
      .then((r) => r.data.sources ?? []),
  queryKnowledge: (source: string, query: string, limit = 5) =>
    http
      .post<{ source: string; query: string; content?: string; error?: string }>(
        '/knowledge/query',
        { source, query, limit },
      )
      .then((r) => r.data),

  // 看板
  getStatsOverview: () => http.get<StatsOverview>('/stats/overview').then((r) => r.data),
  getStatsDaily: (days = 14) =>
    http
      .get<{ points: DailyPoint[] | null }>('/stats/daily', { params: { days } })
      .then((r) => r.data.points ?? []),
  getHotQuestions: () =>
    http
      .get<{ questions: HotQuestion[] | null }>('/stats/hot-questions')
      .then((r) => r.data.questions ?? []),
};

export function apiErrorMessage(err: unknown): string {
  if (axios.isAxiosError(err)) {
    return (err.response?.data as { error?: string } | undefined)?.error ?? err.message;
  }
  return String(err);
}
