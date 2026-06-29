// 与后端 internal/model、internal/service/session 对齐的类型定义

export type SessionStatus = 'queued' | 'active' | 'closed';
export type UserRole = 'normal' | 'vip' | 'knowledge_staff' | 'knowledge_expert' | 'admin';

export interface User {
  id: string;
  username: string;
  role: UserRole;
  roles: UserRole[];
  maxSessions: number;
  createdAt: string;
  updatedAt: string;
}

export interface LoginResult {
  token: string;
  expiresAt: string;
  user: User;
}

export interface Session {
  id: string;
  clientId: string;
  userId?: string;
  username?: string;
  domainId?: string;
  domainName?: string;
  status: SessionStatus;
  createdAt: string;
  startedAt?: string;
  closedAt?: string;
  closeReason?: string;
  title: string;
}

export interface KnowledgeSource {
  id: string;
  domainId: string;
  name: string;
  type: 'stdio' | 'http' | string;
  url?: string;
  headers?: Record<string, string>;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  enabled: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export interface Domain {
  id: string;
  name: string;
  description?: string;
  defaultAgentId?: string;
  runtimePath?: string;
  enabled: boolean;
  createdAt?: string;
  updatedAt?: string;
  knowledgeSources?: KnowledgeSource[];
}

export interface SessionView extends Session {
  durationSeconds: number;
  waitingSeconds: number;
  position?: number;
  queueLen: number;
  active: number;
  maxActive: number;
  model?: string;
  agentType?: string;
}

export interface PagedSessions {
  sessions: Session[];
  total: number;
  page: number;
  pageSize: number;
}

export type MessageRole = 'user' | 'assistant' | 'system';

export interface ImageAttachment {
  id?: string;
  base64: string;
  data?: string;
  mimeType: string;
  filename?: string;
  width?: number;
  height?: number;
}

export interface Message {
  id: string;
  sessionId: string;
  role: MessageRole;
  content: string;
  toolCalls?: string;
  model?: string;
  agentType?: string;
  createdAt: string;
}

export interface ToolCallRecord {
  toolId: string;
  toolName: string;
  input?: Record<string, unknown>;
}

export type ChunkType =
  | 'text'
  | 'error'
  | 'status'
  | 'thinking'
  | 'tool_use'
  | 'tool_result'
  | 'usage'
  | 'done';

export interface Chunk {
  type: ChunkType;
  content?: string;
  toolName?: string;
  toolId?: string;
  toolInput?: Record<string, unknown>;
  isError?: boolean;
  usage?: {
    inputTokens?: number;
    outputTokens?: number;
  };
}

export type WSEventType =
  | 'chunk'
  | 'queue'
  | 'state'
  | 'idle_warning'
  | 'closed'
  | 'message'
  | 'error';

export interface WSEvent {
  type: WSEventType;
  sessionId: string;
  chunk?: Chunk;
  position?: number;
  queueLen?: number;
  active?: number;
  maxActive?: number;
  session?: SessionView;
  message?: Message;
  reason?: string;
  error?: string;
}

export interface Ticket {
  id: string;
  sessionId: string;
  reason: string;
  transcript: string;
  status: 'open' | 'notified' | 'failed';
  createdAt: string;
}

export type CandidateAssetType = 'knowledge' | 'faq' | 'wiki';
export type CandidateAssetStatus = 'pending' | 'approved' | 'rejected';
export type KnowledgePublishTarget = 'local' | 'skill' | 'knowledge_base';

export interface CandidateAsset {
  id: string;
  assetType: CandidateAssetType;
  publishTargets?: KnowledgePublishTarget[];
  title: string;
  question?: string;
  content: string;
  evidence?: string;
  sourceSessionId?: string;
  sourceFeedbackId?: string;
  confidence: number;
  status: CandidateAssetStatus;
  reviewer?: string;
  reviewNote?: string;
  createdAt: string;
  updatedAt: string;
}

export type RuntimeLearningAssetType = 'skill' | 'memory';
export type RuntimeLearningChangeType = 'new' | 'modified' | 'deleted';
export type RuntimeLearningStatus =
  | 'pending_review'
  | 'kept'
  | 'modified'
  | 'deleted'
  | 'converted'
  | 'prohibited_as_evidence';

export interface RuntimeLearningAsset {
  id: string;
  agentType: string;
  assetType: RuntimeLearningAssetType;
  path: string;
  contentHash: string;
  content?: string;
  changeType: RuntimeLearningChangeType;
  riskFlags?: string;
  status: RuntimeLearningStatus;
  reviewer?: string;
  reviewNote?: string;
  createdAt: string;
  updatedAt: string;
}

export type HermesLearningAssetType = RuntimeLearningAssetType;
export type HermesLearningChangeType = RuntimeLearningChangeType;
export type HermesLearningStatus = RuntimeLearningStatus;
export type HermesLearningAsset = RuntimeLearningAsset;

export type LearningJobStatus = 'running' | 'succeeded' | 'failed' | 'skipped';

export interface LearningJob {
  id: string;
  source: string;
  status: LearningJobStatus;
  inputSessions: number;
  outputAssets: number;
  error?: string;
  startedAt: string;
  finishedAt?: string;
}

export interface AgentSettings {
  type: string;
  cliPath: string;
  defaultModel: string;
  apiUrl: string;
  apiToken: string;
  systemPrompt: string;
  supportsMultimodal: boolean;
  updatedAt?: string;
}

export interface AgentCapabilities {
  type: string;
  defaultModel: string;
  supportsMultimodal: boolean;
}

export interface AgentProfile {
  id: string;
  name: string;
  settings: AgentSettings;
}

export interface AgentProfilesSettings {
  activeProfileId: string;
  profiles: AgentProfile[];
  updatedAt?: string;
}

export interface PoolSettings {
  maxActive: number;
  maxQueue: number;
  updatedAt?: string;
}

export interface StatsOverview {
  activeSessions: number;
  queuedSessions: number;
  sessionsToday: number;
  sessions7d: number;
  userMessages7d: number;
  knowledgeHits7d: number;
  knowledgeHitRate: number;
  feedbackUp7d: number;
  feedbackDown7d: number;
  satisfactionRate: number;
  tickets7d: number;
  handoffRate: number;
}

export interface DailyPoint {
  date: string;
  sessions: number;
  up: number;
  down: number;
}

export interface HotQuestion {
  keyword: string;
  count: number;
}
