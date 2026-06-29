// 后端 API 客户端
import axios from 'axios';
import type {
  AgentProfilesSettings,
  AgentCapabilities,
  CandidateAsset,
  CandidateAssetStatus,
  KnowledgePublishTarget,
  DailyPoint,
  HotQuestion,
  Domain,
  KnowledgeSource,
  RuntimeLearningAsset,
  RuntimeLearningStatus,
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

function authHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  const token = localStorage.getItem('callme_auth_token');
  if (token) headers.Authorization = `Bearer ${token}`;
  const activeRole = localStorage.getItem('callme_active_role');
  if (activeRole) headers['X-Callme-Active-Role'] = activeRole;
  return headers;
}

http.interceptors.request.use((config) => {
  const token = localStorage.getItem('callme_auth_token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  const activeRole = localStorage.getItem('callme_active_role');
  if (activeRole) {
    config.headers['X-Callme-Active-Role'] = activeRole;
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
  createSession: (domainId?: string) =>
    http.post<SessionView>('/sessions', { domainId }, { timeout: 90000 }).then((r) => r.data),
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

  // 领域
  listDomains: (includeDisabled = false) =>
    http.get<{ domains: Domain[] | null }>('/domains', { params: includeDisabled ? { includeDisabled: true } : {} })
      .then((r) => r.data.domains ?? []),
  getDomain: (id: string) => http.get<Domain>(`/domains/${id}`).then((r) => r.data),
  upsertDomain: (domain: Domain) => http.put<Domain>(`/domains/${domain.id}`, domain).then((r) => r.data),
  upsertKnowledgeSource: (domainId: string, source: KnowledgeSource) =>
    http.put<Domain>(`/domains/${domainId}/sources/${source.id || 'new'}`, source).then((r) => r.data),
  deleteKnowledgeSource: (domainId: string, sourceId: string) =>
    http.delete<Domain>(`/domains/${domainId}/sources/${sourceId}`).then((r) => r.data),

  // 反馈
  getAgentCapabilities: () =>
    http.get<AgentCapabilities>('/agent/capabilities').then((r) => r.data),
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
    publishTargets: KnowledgePublishTarget[];
    description: string;
    images?: { base64: string; data?: string; mimeType: string; filename?: string; width?: number; height?: number }[];
  }) =>
    http.post<CandidateAsset>('/learning/manual-drafts', payload, { timeout: 90000 }).then((r) => r.data),
  streamManualKnowledgeDraft: async (
    payload: {
      publishTargets: KnowledgePublishTarget[];
      description: string;
      images?: { base64: string; data?: string; mimeType: string; filename?: string; width?: number; height?: number }[];
    },
    onEvent: (event: { type: string; delta?: string; content?: string; candidate?: CandidateAsset; error?: string }) => void,
  ) => {
    const resp = await fetch('/api/v1/learning/manual-drafts/stream', {
      method: 'POST',
      headers: {
        ...authHeaders(),
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
      },
      body: JSON.stringify(payload),
    });
    if (!resp.ok || !resp.body) {
      const text = await resp.text().catch(() => '');
      throw new Error(text || `HTTP ${resp.status}`);
    }
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split('\n\n');
      buffer = frames.pop() ?? '';
      for (const frame of frames) {
        const line = frame.split('\n').find((item) => item.startsWith('data:'));
        if (!line) continue;
        const raw = line.slice(5).trim();
        if (!raw) continue;
        const event = JSON.parse(raw) as { type: string; delta?: string; content?: string; candidate?: CandidateAsset; error?: string };
        onEvent(event);
        if (event.type === 'error') throw new Error(event.error || '生成候选知识失败');
      }
    }
  },
  listRuntimeLearningAssets: (status?: RuntimeLearningStatus) =>
    http
      .get<{ assets: RuntimeLearningAsset[] | null }>('/learning/runtime-assets', {
        params: status ? { status } : {},
      })
      .then((r) => r.data.assets ?? []),
  reviewRuntimeLearningAsset: (
    id: string,
    action: 'keep' | 'delete' | 'modify',
    note?: string,
    content?: string,
  ) =>
    http.post<RuntimeLearningAsset>(`/learning/runtime-assets/${id}/review`, { action, note, content }).then((r) => r.data),
  assistRuntimeLearningEdit: (id: string, instruction: string, content: string) =>
    http
      .post<{ content: string }>(`/learning/runtime-assets/${id}/assist-edit`, { instruction, content }, { timeout: 90000 })
      .then((r) => r.data.content),
  streamRuntimeLearningEdit: async (
    id: string,
    instruction: string,
    content: string,
    onEvent: (event: { type: string; delta?: string; content?: string; error?: string }) => void,
  ) => {
    const resp = await fetch(`/api/v1/learning/runtime-assets/${id}/assist-edit/stream`, {
      method: 'POST',
      headers: {
        ...authHeaders(),
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
      },
      body: JSON.stringify({ instruction, content }),
    });
    if (!resp.ok || !resp.body) {
      const text = await resp.text().catch(() => '');
      throw new Error(text || `HTTP ${resp.status}`);
    }
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split('\n\n');
      buffer = frames.pop() ?? '';
      for (const frame of frames) {
        const line = frame.split('\n').find((item) => item.startsWith('data:'));
        if (!line) continue;
        const raw = line.slice(5).trim();
        if (!raw) continue;
        const event = JSON.parse(raw) as { type: string; delta?: string; content?: string; error?: string };
        onEvent(event);
        if (event.type === 'error') throw new Error(event.error || 'AI 修订失败');
      }
    }
  },
  listHermesLearningAssets: (status?: RuntimeLearningStatus) => api.listRuntimeLearningAssets(status),
  reviewHermesLearningAsset: (
    id: string,
    action: 'keep' | 'delete' | 'modify',
    note?: string,
    content?: string,
  ) => api.reviewRuntimeLearningAsset(id, action, note, content),
  assistHermesLearningEdit: (id: string, instruction: string, content: string) =>
    api.assistRuntimeLearningEdit(id, instruction, content),
  streamHermesLearningEdit: (
    id: string,
    instruction: string,
    content: string,
    onEvent: (event: { type: string; delta?: string; content?: string; error?: string }) => void,
  ) => api.streamRuntimeLearningEdit(id, instruction, content, onEvent),
  listLearningJobs: () =>
    http.get<{ jobs: LearningJob[] | null }>('/learning/jobs').then((r) => r.data.jobs ?? []),
  runLearningJob: () => http.post<{ job: LearningJob }>('/learning/jobs/run').then((r) => r.data.job),

  // 工单
  createHandoff: (sessionId: string, reason: string) =>
    http.post<Ticket>(`/sessions/${sessionId}/handoff`, { reason }).then((r) => r.data),
  listTickets: () =>
    http.get<{ tickets: Ticket[] | null }>('/tickets').then((r) => r.data.tickets ?? []),

  // 设置
  getAgentSettings: () => http.get<AgentProfilesSettings>('/settings/agent').then((r) => r.data),
  updateAgentSettings: (s: AgentProfilesSettings) =>
    http.put<AgentProfilesSettings>('/settings/agent', s).then((r) => r.data),
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
