// 后端 API 客户端
import axios from 'axios';
import type {
  AgentSettings,
  CandidateAsset,
  CandidateAssetStatus,
  CandidateAssetType,
  DailyPoint,
  HotQuestion,
  HermesLearningAsset,
  HermesLearningStatus,
  LearningJob,
  LoginResult,
  Message,
  PagedSessions,
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
  me: () => http.get<{ user: User; version?: string }>('/auth/me').then((r) => r.data),
  listUsers: () => http.get<{ users: User[] | null }>('/users').then((r) => r.data.users ?? []),
  updateUserRole: (id: string, roles: UserRole[], maxSessions?: number) =>
    http.put(`/users/${id}/role`, { roles, maxSessions }),
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
  listClosedSessions: (params: { start?: string; end?: string; userId?: string; page: number; pageSize: number }) =>
    http
      .get<{ sessions: Session[] | null; total: number; page: number; pageSize: number }>(
        '/admin/sessions/closed',
        { params },
      )
      .then((r): PagedSessions => ({
        sessions: r.data.sessions ?? [],
        total: r.data.total ?? 0,
        page: r.data.page,
        pageSize: r.data.pageSize,
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

  // 自学习沙箱：候选资产审批
  listCandidates: (status?: CandidateAssetStatus) =>
    http
      .get<{ candidates: CandidateAsset[] | null }>('/learning/candidates', {
        params: status ? { status } : {},
      })
      .then((r) => r.data.candidates ?? []),
  updateCandidate: (id: string, payload: Partial<CandidateAsset>) =>
    http.put<CandidateAsset>(`/learning/candidates/${id}`, payload).then((r) => r.data),
  reviewCandidate: (id: string, approve: boolean, note?: string) =>
    http
      .post<CandidateAsset>(`/learning/candidates/${id}/review`, { approve, note })
      .then((r) => r.data),
  createManualKnowledgeDraft: (payload: {
    assetType: CandidateAssetType;
    description: string;
    images?: { base64: string; data?: string; mimeType: string; filename?: string; width?: number; height?: number }[];
  }) =>
    http.post<CandidateAsset>('/learning/manual-drafts', payload, { timeout: 90000 }).then((r) => r.data),
  listHermesLearningAssets: (status?: HermesLearningStatus) =>
    http
      .get<{ assets: HermesLearningAsset[] | null }>('/learning/hermes-assets', {
        params: status ? { status } : {},
      })
      .then((r) => r.data.assets ?? []),
  listLearningJobs: () =>
    http.get<{ jobs: LearningJob[] | null }>('/learning/jobs').then((r) => r.data.jobs ?? []),
  runLearningJob: () => http.post('/learning/jobs/run'),

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
