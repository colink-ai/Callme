// 聊天会话状态（zustand）：会话生命周期 + WebSocket 流式事件处理
import { create } from 'zustand';
import { api, apiErrorMessage } from '../api/client';
import type { Chunk, ImageAttachment, Message, SessionView, ToolCallRecord, WSEvent } from '../types';

export interface ChatMessage {
  id: string; // 落库后的 messageId（反馈用）；流式期间为临时 id
  role: 'user' | 'assistant';
  content: string;
  citations: ToolCallRecord[]; // 知识检索引用
  images?: ImageAttachment[];
  streaming?: boolean;
  feedback?: 'up' | 'down';
  model?: string; // 生成本条回答的模型（消息卡片标签）
  agentType?: string; // 生成本条回答的 Agent 类型（消息卡片标签）
  createdAt?: string;
  steps?: AgentStep[];
}

interface ChatState {
  session: SessionView | null;
  connected: boolean;
  restoring: boolean;
  starting: boolean;
  position: number; // 排队位置，0 表示未排队
  queueLen: number;
  messages: ChatMessage[];
  thinking: boolean; // Agent 思考/检索中（尚无文本输出）
  busy: boolean; // 一轮回答进行中
  idleWarning: boolean;
  closedReason: string | null;
  error: string | null;

  restoreSession: () => Promise<void>;
  startSession: () => Promise<void>;
  continueSession: (sessionId: string) => Promise<void>;
  sendMessage: (content: string, images?: ImageAttachment[]) => void;
  stopGeneration: () => void;
  endSession: () => Promise<void>;
  requestHandoff: (reason: string) => Promise<string>;
  submitFeedback: (messageId: string, rating: 'up' | 'down', correction?: string) => Promise<void>;
  reset: () => void;
}

export interface AgentStep {
  id: string;
  type: 'thinking' | 'tool' | 'usage' | 'status';
  label: string;
  detail?: string;
  status: 'running' | 'done' | 'error';
}

let socket: WebSocket | null = null;
let pingTimer: number | null = null;
let userClosingSession = false;

function closeSocket() {
  if (pingTimer) {
    window.clearInterval(pingTimer);
    pingTimer = null;
  }
  if (socket) {
    socket.onclose = null;
    socket.close();
    socket = null;
  }
}

export const useChatStore = create<ChatState>((set, get) => ({
  session: null,
  connected: false,
  restoring: false,
  starting: false,
  position: 0,
  queueLen: 0,
  messages: [],
  thinking: false,
  busy: false,
  idleWarning: false,
  closedReason: null,
  error: null,

  restoreSession: async () => {
    const current = get().session;
    if ((current && current.status !== 'closed' && !get().closedReason && get().connected) || get().restoring) return;
    set({ restoring: true, error: null });
    try {
      const view = await api.getCurrentSession();
      if (!view) {
        set({
          session: null,
          connected: false,
          position: 0,
          queueLen: 0,
          busy: false,
          thinking: false,
        });
        closeSocket();
        return;
      }

      const restoredMessages = await api.listMessages(view.id);
      set({
        session: view,
        position: view.position ?? 0,
        queueLen: view.queueLen ?? 0,
        messages: restoredMessages.map(toChatMessage),
        closedReason: null,
      });
      connectWS(view.id, set, get);
    } catch (err) {
      set({ error: apiErrorMessage(err) });
    } finally {
      set({ restoring: false });
    }
  },

  startSession: async () => {
    if (get().starting) return;
    const current = get().session;
    const canReuseCurrent = current && current.status !== 'closed' && !get().closedReason && get().connected;
    set({
      starting: true,
      error: null,
      closedReason: null,
      messages: canReuseCurrent ? get().messages : [],
      position: canReuseCurrent ? get().position : 0,
      queueLen: canReuseCurrent ? get().queueLen : 0,
      session: canReuseCurrent ? current : null,
      busy: false,
      thinking: false,
    });
    try {
      if (canReuseCurrent) {
        await get().restoreSession();
        return;
      }

      const view = await api.createSession();
      set({ session: view, position: view.position ?? 0, queueLen: view.queueLen ?? 0 });
      connectWS(view.id, set, get);
    } catch (err) {
      const message = apiErrorMessage(err);
      if (message.includes('已有进行中的会话')) {
        set({ session: null, closedReason: null, starting: false });
        await get().restoreSession();
        return;
      }
      set({ error: message });
    } finally {
      set({ starting: false });
    }
  },

  continueSession: async (sessionId: string) => {
    if (get().starting) return;
    closeSocket();
    set({
      starting: true,
      error: null,
      closedReason: null,
      messages: [],
      position: 0,
      queueLen: 0,
      session: null,
      connected: false,
      busy: false,
      thinking: false,
    });
    try {
      const view = await api.continueSession(sessionId);
      const restoredMessages = await api.listMessages(view.id);
      set({
        session: view,
        position: view.position ?? 0,
        queueLen: view.queueLen ?? 0,
        messages: restoredMessages.map(toChatMessage),
        closedReason: null,
      });
      connectWS(view.id, set, get);
    } catch (err) {
      const message = apiErrorMessage(err);
      if (message.includes('已有进行中的会话')) {
        set({ starting: false });
        await get().restoreSession();
        return;
      }
      set({ error: message });
    } finally {
      set({ starting: false });
    }
  },

  sendMessage: (content: string, images) => {
    const { session, busy } = get();
    if (!session || !socket || socket.readyState !== WebSocket.OPEN || busy) return;
    set((s) => ({
      messages: [
        ...s.messages,
        { id: `tmp-user-${Date.now()}`, role: 'user', content, citations: [], images },
      ],
      busy: true,
      thinking: true,
      idleWarning: false,
      error: null,
    }));
    socket.send(JSON.stringify({
      type: 'user_message',
      content,
      images: images?.map((img) => ({
        mimeType: img.mimeType,
        data: img.base64 || img.data,
        filename: img.filename,
        width: img.width,
        height: img.height,
      })),
    }));
  },

  stopGeneration: () => {
    const { session, busy } = get();
    if (!session || !socket || socket.readyState !== WebSocket.OPEN || !busy) return;
    socket.send(JSON.stringify({ type: 'stop' }));
    set((s) => ({
      error: null,
      busy: false,
      thinking: false,
      messages: finalizeStreamingMessages(s.messages, true),
    }));
  },

  endSession: async () => {
    const { session } = get();
    if (!session) return;
    userClosingSession = true;
    set((s) => ({
      error: null,
      busy: false,
      thinking: false,
      messages: finalizeStreamingMessages(s.messages, session.status === 'active'),
    }));
    try {
      await api.closeSession(session.id);
    } finally {
      closeSocket();
      const reason = session.status === 'queued' && get().position > 0 ? 'queue_leave' : 'user';
      set({
        connected: false,
        busy: false,
        thinking: false,
        closedReason: reason,
        session: {
          ...session,
          status: 'closed',
          closeReason: reason,
          closedAt: new Date().toISOString(),
        },
      });
      userClosingSession = false;
    }
  },

  requestHandoff: async (reason: string) => {
    const { session } = get();
    if (!session) throw new Error('无进行中的会话');
    const ticket = await api.createHandoff(session.id, reason);
    return ticket.id;
  },

  submitFeedback: async (messageId, rating, correction) => {
    const { session } = get();
    if (!session) return;
    await api.submitFeedback({ sessionId: session.id, messageId, rating, correction });
    set((s) => ({
      messages: s.messages.map((m) => (m.id === messageId ? { ...m, feedback: rating } : m)),
    }));
  },

  reset: () => {
    closeSocket();
    set({
      session: null,
      connected: false,
      restoring: false,
      starting: false,
      position: 0,
      queueLen: 0,
      messages: [],
      thinking: false,
      busy: false,
      idleWarning: false,
      closedReason: null,
      error: null,
    });
  },
}));

type Setter = (fn: Partial<ChatState> | ((s: ChatState) => Partial<ChatState>)) => void;
type Getter = () => ChatState;

function connectWS(sessionId: string, set: Setter, get: Getter) {
  closeSocket();
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  const token = encodeURIComponent(localStorage.getItem('callme_auth_token') ?? '');
  socket = new WebSocket(`${proto}://${window.location.host}/ws/${sessionId}?token=${token}`);

  socket.onopen = () => {
    set({ connected: true });
    // 心跳：保活 + 告知服务端用户在线（防空闲回收误判）
    pingTimer = window.setInterval(() => {
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'ping' }));
      }
    }, 30000);
  };

  socket.onclose = () => set({ connected: false });

  socket.onmessage = (raw) => {
    let ev: WSEvent;
    try {
      ev = JSON.parse(raw.data);
    } catch {
      return;
    }

    switch (ev.type) {
      case 'state':
        set((s) => ({
          session: ev.session ?? null,
          position: ev.position ?? ev.session?.position ?? 0,
          queueLen: ev.session?.queueLen ?? s.queueLen,
        }));
        break;

      case 'queue':
        set((s) => ({
          position: ev.position ?? 0,
          queueLen: ev.queueLen ?? 0,
          session: s.session
            ? {
                ...s.session,
                position: ev.position ?? s.session.position,
                queueLen: ev.queueLen ?? s.session.queueLen,
                active: ev.active ?? s.session.active,
                maxActive: ev.maxActive ?? s.session.maxActive,
              }
            : s.session,
        }));
        break;

      case 'chunk':
        if (ev.chunk) handleChunk(ev.chunk, set, get);
        break;

      case 'message':
        // 落库通知：把临时消息 id 替换为真实 messageId（反馈定位用）
        if (ev.message) {
          const msg = ev.message;
          set((s) => {
            const messages = [...s.messages];
            for (let i = messages.length - 1; i >= 0; i--) {
              if (messages[i].role === msg.role && messages[i].id.startsWith('tmp-')) {
                messages[i] = {
                  ...messages[i],
                  id: msg.id,
                  content: msg.content || messages[i].content,
                  citations: msg.role === 'assistant' ? parseToolCalls(msg.toolCalls) || messages[i].citations : [],
                  images: msg.role === 'user' ? parseMessageImages(msg) || messages[i].images : messages[i].images,
                  streaming: false,
                  model: msg.model,
                  agentType: msg.agentType,
                  createdAt: msg.createdAt,
                };
                break;
              }
            }
            return { messages };
          });
        }
        break;

      case 'idle_warning':
        set({ idleWarning: true });
        break;

      case 'closed':
        set({
          closedReason: ev.reason ?? 'closed',
          busy: false,
          thinking: false,
          error: null,
          messages: finalizeStreamingMessages(get().messages, ev.reason === 'user'),
          session: ev.session ?? get().session,
        });
        closeSocket();
        set({ connected: false });
        break;

      case 'error':
        if (userClosingSession || isCancellationError(ev.error)) {
          set((s) => ({
            error: null,
            busy: false,
            thinking: false,
            messages: finalizeStreamingMessages(s.messages, userClosingSession),
          }));
          break;
        }
        set((s) => ({
          error: ev.error ?? '未知错误',
          busy: false,
          thinking: false,
          // 同时收尾卡在"正在生成回复"的空气泡，避免永远转圈
          messages: finalizeStreamingMessages(s.messages, false),
        }));
        break;
    }
  };
}

function handleChunk(chunk: Chunk, set: Setter, get: Getter) {
  switch (chunk.type) {
    case 'thinking':
      set((s) => {
        const messages = ensureAssistantMessage(s.messages);
        const last = messages[messages.length - 1];
        messages[messages.length - 1] = {
          ...last,
          steps: upsertStep(last.steps, {
            id: 'thinking',
            type: 'thinking',
            label: chunk.content || '正在思考',
            status: 'running',
          }),
        };
        return { messages, thinking: true };
      });
      break;

    case 'text': {
      set((s) => {
        const messages = markStepDone(ensureAssistantMessage(s.messages), 'thinking');
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant' && last.streaming) {
          messages[messages.length - 1] = { ...last, content: last.content + (chunk.content ?? '') };
        } else {
          messages.push({
            id: `tmp-assistant-${Date.now()}`,
            role: 'assistant',
            content: chunk.content ?? '',
            citations: [],
            streaming: true,
          });
        }
        return { messages, thinking: false };
      });
      break;
    }

    case 'tool_use': {
      // 知识检索调用：作为引用标签展示在当前回答上
      set((s) => {
        const messages = ensureAssistantMessage(s.messages);
        const last = messages[messages.length - 1];
        const citation: ToolCallRecord = {
          toolId: chunk.toolId ?? '',
          toolName: chunk.toolName ?? 'knowledge',
          input: chunk.toolInput,
        };
        // 同一 toolId 去重（tool_call_update 会重复推送）
        const citations = last.citations.some((c) => c.toolId === citation.toolId && citation.toolId)
          ? last.citations
          : [...last.citations, citation];
        messages[messages.length - 1] = {
          ...last,
          citations,
          steps: upsertStep(last.steps, {
            id: citation.toolId || `tool-${last.steps?.length ?? 0}`,
            type: 'tool',
            label: prettyToolName(citation.toolName),
            detail: summarizeInput(citation.input),
            status: 'running',
          }),
        };
        return { messages, thinking: true };
      });
      break;
    }

    case 'tool_result':
      set((s) => {
        const messages = ensureAssistantMessage(s.messages);
        const last = messages[messages.length - 1];
        const id = chunk.toolId || 'tool-result';
        messages[messages.length - 1] = {
          ...last,
          steps: upsertStep(last.steps, {
            id,
            type: 'tool',
            label: stepLabel(last.steps, id) || '工具调用',
            detail: summarizeResult(chunk.content),
            status: chunk.isError ? 'error' : 'done',
          }),
        };
        return { messages };
      });
      break;

    case 'usage':
      set((s) => {
        const messages = ensureAssistantMessage(s.messages);
        const last = messages[messages.length - 1];
        const usage = chunk.usage;
        messages[messages.length - 1] = {
          ...last,
          steps: upsertStep(last.steps, {
            id: 'usage',
            type: 'usage',
            label: 'Token 用量',
            detail: usage ? `输入 ${usage.inputTokens ?? 0} / 输出 ${usage.outputTokens ?? 0}` : undefined,
            status: 'done',
          }),
        };
        return { messages };
      });
      break;

    case 'done':
      set((s) => {
        const messages = s.messages.map((m, i) =>
          i === s.messages.length - 1
            ? { ...m, streaming: false, steps: m.steps?.map((step): AgentStep => step.status === 'running' ? { ...step, status: 'done' } : step) }
            : m,
        );
        return { messages, busy: false, thinking: false };
      });
      break;

    case 'error':
      if (userClosingSession || isCancellationError(chunk.content)) {
        set((s) => ({
          error: null,
          busy: false,
          thinking: false,
          messages: finalizeStreamingMessages(s.messages, userClosingSession),
        }));
        break;
      }
      set({ error: chunk.content ?? 'Agent 错误', busy: false, thinking: false });
      break;

    case 'status':
      set((s) => {
        const messages = ensureAssistantMessage(s.messages);
        const last = messages[messages.length - 1];
        messages[messages.length - 1] = {
          ...last,
          steps: upsertStep(last.steps, {
            id: `status-${Date.now()}`,
            type: 'status',
            label: chunk.content || '状态更新',
            status: 'done',
          }),
        };
        return { messages };
      });
      break;

    default:
      break;
  }
  void get;
}

function ensureAssistantMessage(messages: ChatMessage[]): ChatMessage[] {
  const next = [...messages];
  const last = next[next.length - 1];
  if (last && last.role === 'assistant' && last.streaming) return next;
  next.push({
    id: `tmp-assistant-${Date.now()}`,
    role: 'assistant',
    content: '',
    citations: [],
    streaming: true,
    steps: [],
  });
  return next;
}

function finalizeStreamingMessages(messages: ChatMessage[], stoppedByUser: boolean): ChatMessage[] {
  return messages.map((m, i) => {
    if (i !== messages.length - 1 || m.role !== 'assistant' || !m.streaming) return m;
    const steps = (m.steps ?? []).map((step) =>
      step.status === 'running' ? { ...step, status: 'done' as const } : step,
    );
    const statusStep: AgentStep | null = stoppedByUser
      ? {
          id: 'stopped',
          type: 'status',
          label: '已停止生成',
          status: 'done',
        }
      : null;
    const nextSteps = statusStep ? upsertStep(steps, statusStep) : steps;
    // 内容为空时给兜底文案，否则空气泡会一直显示"正在生成回复"转圈
    const fallback = stoppedByUser ? '已停止生成' : '回答已中断，请重试';
    return {
      ...m,
      content: m.content || fallback,
      streaming: false,
      steps: nextSteps,
    };
  });
}

function isCancellationError(text?: string): boolean {
  if (!text) return false;
  const lower = text.toLowerCase();
  return lower.includes('context canceled')
    || lower.includes('request session/prompt aborted')
    || lower.includes('aborted');
}

function parseToolCalls(raw?: string): ToolCallRecord[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function parseMessageImages(msg: Message): ImageAttachment[] {
  if (msg.role !== 'user' || !msg.toolCalls) return [];
  try {
    const parsed = JSON.parse(msg.toolCalls);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((img) => typeof img?.mimeType === 'string' && typeof img?.data === 'string')
      .map((img, index) => ({
        id: `${msg.id}-image-${index}`,
        base64: img.data,
        data: img.data,
        mimeType: img.mimeType,
        filename: img.filename,
        width: img.width,
        height: img.height,
      }));
  } catch {
    return [];
  }
}

export function toChatMessage(msg: Message): ChatMessage {
  return {
    id: msg.id,
    role: msg.role === 'assistant' ? 'assistant' : 'user',
    content: msg.content,
    citations: msg.role === 'assistant' ? parseToolCalls(msg.toolCalls) : [],
    images: parseMessageImages(msg),
    model: msg.model,
    agentType: msg.agentType,
    createdAt: msg.createdAt,
  };
}

function upsertStep(steps: AgentStep[] | undefined, next: AgentStep): AgentStep[] {
  const list = steps ? [...steps] : [];
  const idx = list.findIndex((s) => s.id === next.id);
  if (idx >= 0) {
    list[idx] = { ...list[idx], ...next };
  } else {
    list.push(next);
  }
  return list.slice(-8);
}

function markStepDone(messages: ChatMessage[], stepID: string): ChatMessage[] {
  const last = messages[messages.length - 1];
  if (!last?.steps?.some((s) => s.id === stepID)) return messages;
  messages[messages.length - 1] = {
    ...last,
    steps: last.steps.map((s): AgentStep => s.id === stepID ? { ...s, status: 'done' } : s),
  };
  return messages;
}

function stepLabel(steps: AgentStep[] | undefined, id: string): string | undefined {
  return steps?.find((s) => s.id === id)?.label;
}

function prettyToolName(name: string): string {
  if (name.startsWith('mcp_')) {
    const body = name.slice(4);
    if (body.endsWith('_list_resources')) {
      return `检查 ${body.replace(/_list_resources$/, '').replace(/_/g, '-')} 资源`;
    }
    return `检索 ${body.replace(/_(query|search|lookup|get)$/, '').replace(/_/g, '-')}`;
  }
  if (name.includes(':')) return name;
  return `调用 ${name}`;
}

function summarizeInput(input?: Record<string, unknown>): string | undefined {
  if (!input) return undefined;
  const query = input.query;
  if (typeof query === 'string' && query) return query;
  return JSON.stringify(input).slice(0, 160);
}

function summarizeResult(content?: string): string | undefined {
  if (!content) return undefined;
  let text = content;
  try {
    const parsed = JSON.parse(content);
    if (typeof parsed.result === 'string') text = parsed.result;
    else if (typeof parsed.error === 'string') text = parsed.error;
    else if (typeof parsed.content === 'string') text = parsed.content;
    else if (parsed.success && parsed.name) text = `已加载 ${parsed.name}`;
  } catch {
    // 保留原始文本
  }
  text = text.replace(/\s+/g, ' ').trim();
  return text.length > 180 ? `${text.slice(0, 180)}…` : text;
}
