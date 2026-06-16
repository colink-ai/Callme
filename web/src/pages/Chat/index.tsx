// 用户端聊天页：排队状态、会话计时、流式回答、知识引用、反馈、转人工
import { useCallback, useEffect, useRef, useState, type ClipboardEvent, type DragEvent } from 'react';
import {
  Alert,
  Button,
  Collapse,
  Empty,
  Image,
  Input,
  Modal,
  Popconfirm,
  Space,
  Spin,
  Tag,
  Tooltip,
  Typography,
  Upload,
  message as antMessage,
} from 'antd';
import {
  ClockCircleOutlined,
  CustomerServiceOutlined,
  DislikeFilled,
  DislikeOutlined,
  LikeFilled,
  LikeOutlined,
  PlusOutlined,
  PoweroffOutlined,
  SendOutlined,
  StopOutlined,
  TeamOutlined,
  FileSearchOutlined,
  ToolOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  DeleteOutlined,
  LoadingOutlined,
  PlayCircleOutlined,
  PictureOutlined,
  CloseOutlined,
  CopyOutlined,
  DownOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
} from '@ant-design/icons';
import { Avatar, List } from 'antd';
import { UserOutlined } from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import dayjs from 'dayjs';
import { useChatStore, toChatMessage, type AgentStep, type ChatMessage } from '../../store/chatStore';
import { api, apiErrorMessage } from '../../api/client';
import type { ImageAttachment, Session } from '../../types';
import { LogoIcon } from '../../components/Logo';
import HermesIcon from '../../components/HermesIcon';

const { Text, Title } = Typography;

// 生成中提示：显示已等待秒数，避免慢响应（如图片推理）让用户误以为卡死
function GeneratingHint() {
  const [secs, setSecs] = useState(0);
  useEffect(() => {
    const t = window.setInterval(() => setSecs((n) => n + 1), 1000);
    return () => window.clearInterval(t);
  }, []);
  return (
    <Space size={8}>
      <span className="thinking-dots"><span /><span /><span /></span>
      <Text type="secondary">
        正在生成回复…{secs >= 5 ? ` 已等待 ${secs}s${secs >= 20 ? '（含图片等复杂输入时较慢，请稍候）' : ''}` : ''}
      </Text>
    </Space>
  );
}

const ASSISTANT_DISPLAY_NAME = 'Callme 助手';
const ASSISTANT_FULL_NAME = 'Callme 智能问题解决助手';
const MAX_IMAGES = 4;
const MAX_IMAGE_SIZE = 10 * 1024 * 1024;
const AUTO_SCROLL_BOTTOM_THRESHOLD = 80;

function formatAgentType(type?: string): string {
  if (!type) return '';
  const normalized = type.replace(/[_-]/g, '').toLowerCase();
  if (normalized === 'opencode') return 'OpenCode';
  if (normalized === 'hermes') return 'Hermes';
  return type
    .split(/[_-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
}

function formatDuration(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  return `${m}:${String(s).padStart(2, '0')}`;
}

// 会话开始时间 + 实时持续时长（坐席制要求显式展示）
function SessionTimer({ startedAt }: { startedAt: string }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = window.setInterval(() => tick((n) => n + 1), 1000);
    return () => window.clearInterval(t);
  }, []);
  const elapsed = Math.max(0, Math.floor((Date.now() - dayjs(startedAt).valueOf()) / 1000));
  return (
    <Space size={12}>
      <Tag icon={<ClockCircleOutlined />} color="green">
        开始于 {dayjs(startedAt).format('HH:mm:ss')}
      </Tag>
      <Tag color="processing">已持续 {formatDuration(elapsed)}</Tag>
    </Space>
  );
}

// 知识源工具名美化：Hermes 把 MCP 工具命名为 mcp_<server>_<tool>
// 例：mcp_wiki_graph_query → Wiki 知识图谱
const KB_NAME_MAP: Record<string, string> = {
  wiki_graph: 'Wiki 知识图谱',
  code_graph: '代码知识图谱',
};
function prettyKnowledgeName(toolName: string): string | null {
  if (!toolName.startsWith('mcp_')) return null; // 仅展示知识库 MCP 工具，隐藏 Agent 原生工具
  if (!/_(query|search|lookup|get)$/.test(toolName)) return null;
  const body = toolName.slice(4).replace(/_(query|search|lookup|get)$/, '');
  return KB_NAME_MAP[body] ?? body.replace(/_/g, '-');
}

function CitationTags({ msg }: { msg: ChatMessage }) {
  // 只把知识库检索作为「引用来源」展示给用户（过滤掉 Agent 内部工具），按知识源去重
  const byLabel = new Map<string, string[]>();
  for (const c of msg.citations) {
    const label = prettyKnowledgeName(c.toolName);
    if (!label) continue;
    const q = (c.input?.query as string) ?? '';
    const list = byLabel.get(label) ?? [];
    if (q) list.push(q);
    byLabel.set(label, list);
  }
  if (byLabel.size === 0) return null;
  return (
    <div style={{ marginTop: 6 }}>
      <Text type="secondary" style={{ fontSize: 12, marginRight: 4 }}>
        引用知识：
      </Text>
      {[...byLabel.entries()].map(([label, queries]) => (
        <Tooltip key={label} title={queries.length ? `检索：${queries.join('；')}` : label}>
          <span className="citation-tag">
            <FileSearchOutlined /> {label}
          </span>
        </Tooltip>
      ))}
    </div>
  );
}

function MessageImages({ images }: { images?: ImageAttachment[] }) {
  if (!images?.length) return null;
  return (
    <div className="message-image-grid">
      {images.map((img, index) => (
        <Image
          key={img.id ?? `${img.filename ?? 'image'}-${index}`}
          src={`data:${img.mimeType};base64,${img.base64 || img.data}`}
          alt={img.filename || `图片 ${index + 1}`}
          width={96}
          height={96}
          style={{ objectFit: 'cover', borderRadius: 8 }}
        />
      ))}
    </div>
  );
}

function FeedbackBar({ msg }: { msg: ChatMessage }) {
  const submitFeedback = useChatStore((s) => s.submitFeedback);
  const [correctionOpen, setCorrectionOpen] = useState(false);
  const [correction, setCorrection] = useState('');

  if (msg.role !== 'assistant' || msg.streaming || msg.id.startsWith('tmp-')) return null;

  const send = async (rating: 'up' | 'down', text?: string) => {
    try {
      await submitFeedback(msg.id, rating, text);
      antMessage.success(rating === 'up' ? '感谢您的认可！' : '已记录，我们会持续改进');
    } catch (err) {
      antMessage.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ marginTop: 4 }}>
      <Space size={4}>
        <Button
          type="text"
          size="small"
          icon={msg.feedback === 'up' ? <LikeFilled style={{ color: '#10b981' }} /> : <LikeOutlined />}
          disabled={!!msg.feedback}
          onClick={() => send('up')}
        />
        <Button
          type="text"
          size="small"
          icon={msg.feedback === 'down' ? <DislikeFilled style={{ color: '#ef4444' }} /> : <DislikeOutlined />}
          disabled={!!msg.feedback}
          onClick={() => setCorrectionOpen(true)}
        />
      </Space>
      <Modal
        title="告诉我们正确答案（可选）"
        open={correctionOpen}
        onOk={async () => {
          setCorrectionOpen(false);
          await send('down', correction);
          setCorrection('');
        }}
        onCancel={() => setCorrectionOpen(false)}
        okText="提交"
        cancelText="取消"
      >
        <Text type="secondary">你的纠错会沉淀为 FAQ / 排障路径，帮助下次更快定位同类问题。</Text>
        <Input.TextArea
          rows={4}
          style={{ marginTop: 12 }}
          placeholder="期望的正确回答或处理方式…"
          value={correction}
          onChange={(e) => setCorrection(e.target.value)}
        />
      </Modal>
    </div>
  );
}

function StepIcon({ step }: { step: AgentStep }) {
  if (step.status === 'running') return <LoadingOutlined style={{ color: 'var(--color-primary)' }} />;
  if (step.status === 'error') return <CloseCircleOutlined style={{ color: '#ef4444' }} />;
  if (step.type === 'tool') return <ToolOutlined style={{ color: '#64748b' }} />;
  return <CheckCircleOutlined style={{ color: '#10b981' }} />;
}

function AgentSteps({ msg }: { msg: ChatMessage }) {
  if (!msg.steps?.length) return null;
  const running = msg.steps.some((step) => step.status === 'running');
  return (
    <Collapse
      ghost
      size="small"
      className="agent-process-collapse"
      items={[
        {
          key: 'process',
          label: (
            <Space size={6}>
              {running ? <LoadingOutlined /> : <CheckCircleOutlined />}
              <Text type="secondary" style={{ fontSize: 12 }}>
                执行过程（{msg.steps.length}）
              </Text>
            </Space>
          ),
          children: (
            <div className="agent-steps">
              {msg.steps.map((step) => (
                <div key={step.id} className={`agent-step ${step.status}`}>
                  <StepIcon step={step} />
                  <div className="agent-step-text">
                    <Text style={{ fontSize: 12 }}>{step.label}</Text>
                    {step.detail && (
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {step.detail}
                      </Text>
                    )}
                  </div>
                </div>
              ))}
            </div>
          ),
        },
      ]}
    />
  );
}

// 单条消息气泡（实时与历史只读共用）；readOnly 时不显示反馈按钮
function MessageBubble({
  msg,
  sessionModel,
  sessionAgentType,
  readOnly,
}: {
  msg: ChatMessage;
  sessionModel?: string;
  sessionAgentType?: string;
  readOnly?: boolean;
}) {
  const isUser = msg.role === 'user';
  const agentType = formatAgentType(msg.agentType || sessionAgentType);
  const model = msg.model || sessionModel;
  const runtimeLabel = [agentType, model].filter(Boolean).join(' · ');
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: isUser ? 'row-reverse' : 'row',
        alignItems: 'flex-start',
        marginBottom: 18,
      }}
    >
      {isUser ? (
        <Avatar
          size={40}
          icon={<UserOutlined />}
          style={{ backgroundColor: 'var(--color-primary)', marginLeft: 12, flexShrink: 0, boxShadow: '0 2px 8px rgba(0,0,0,0.1)' }}
        />
      ) : (
        <div className="agent-avatar" style={{ marginRight: 12 }} title={ASSISTANT_FULL_NAME}>
          <HermesIcon size={22} />
        </div>
      )}
      <div style={{ maxWidth: '75%' }}>
        {!isUser && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <Text strong style={{ fontSize: 14 }}>{ASSISTANT_DISPLAY_NAME}</Text>
            {runtimeLabel && (
              <Tag color="green" style={{ marginInlineEnd: 0 }}>{runtimeLabel}</Tag>
            )}
            {msg.createdAt && (
              <Text type="secondary" style={{ fontSize: 12 }}>{dayjs(msg.createdAt).format('HH:mm')}</Text>
            )}
          </div>
        )}
        <div className={`chat-bubble ${msg.role}`}>
          {isUser && <MessageImages images={msg.images} />}
          {msg.role === 'assistant' ? (
            <>
              <AgentSteps msg={msg} />
              <div className={msg.streaming ? 'streaming-cursor' : ''}>
                {msg.content ? (
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.content}</ReactMarkdown>
                ) : (
                  <GeneratingHint />
                )}
              </div>
            </>
          ) : (
            msg.content || (msg.images?.length ? '已发送图片' : '')
          )}
        </div>
        {!isUser && <CitationTags msg={msg} />}
        {!isUser && !readOnly && <FeedbackBar msg={msg} />}
      </div>
    </div>
  );
}

// 左侧历史会话栏
function HistorySidebar({
  sessions,
  activeId,
  onSelect,
  onContinue,
  onDelete,
  onNew,
  onCollapse,
}: {
  sessions: Session[];
  activeId: string | null;
  onSelect: (s: Session) => void;
  onContinue: (s: Session) => void;
  onDelete: (s: Session) => void;
  onNew: () => void;
  onCollapse: () => void;
}) {
  const copySessionId = async (session: Session) => {
    try {
      await navigator.clipboard.writeText(session.id);
      antMessage.success('会话 ID 已复制');
    } catch {
      antMessage.error('复制失败，请手动复制会话 ID');
    }
  };

  return (
    <div className="history-sidebar">
      <div className="history-sidebar-head">
        <Space size={4}>
          <Tooltip title="收起历史栏">
            <Button type="text" size="small" icon={<MenuFoldOutlined />} onClick={onCollapse} />
          </Tooltip>
          <Text strong>历史会话</Text>
        </Space>
        <Button type="text" size="small" icon={<PlusOutlined />} onClick={onNew}>
          新会话
        </Button>
      </div>
      <div className="history-sidebar-list">
        {sessions.length === 0 ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无历史" style={{ marginTop: 40 }} />
        ) : (
          <List
            dataSource={sessions}
            split={false}
            renderItem={(s) => (
              <div
                className={`history-item ${activeId === s.id ? 'active' : ''}`}
                onClick={() => onSelect(s)}
              >
                <div className="history-item-title">{s.title || '未命名会话'}</div>
                <div className="history-item-meta">
                  <span className={`history-dot ${s.status}`} />
                  {dayjs(s.createdAt).format('MM-DD HH:mm')}
                </div>
                <div className="history-item-actions" onClick={(e) => e.stopPropagation()}>
                  <Tooltip title="复制会话 ID">
                    <Button
                      type="text"
                      size="small"
                      icon={<CopyOutlined />}
                      onClick={() => copySessionId(s)}
                    />
                  </Tooltip>
                  {s.status === 'closed' && (
                    <>
                      <Tooltip title="继续问答">
                        <Button
                          type="text"
                        size="small"
                        icon={<PlayCircleOutlined />}
                        onClick={() => onContinue(s)}
                      />
                    </Tooltip>
                    <Popconfirm
                      title="删除该历史会话？"
                      description="会同时删除该会话的消息、反馈和工单记录。"
                      okText="删除"
                      cancelText="取消"
                      okButtonProps={{ danger: true }}
                      onConfirm={() => onDelete(s)}
                    >
                      <Tooltip title="删除历史">
                        <Button type="text" danger size="small" icon={<DeleteOutlined />} />
                      </Tooltip>
                    </Popconfirm>
                    </>
                  )}
                </div>
              </div>
            )}
          />
        )}
      </div>
    </div>
  );
}

export default function ChatPage() {
  const {
    session,
    position,
    queueLen,
    messages,
    restoring,
    thinking,
    busy,
    idleWarning,
    closedReason,
    error,
    restoreSession,
    startSession,
    continueSession,
    sendMessage,
    endSession,
    requestHandoff,
  } = useChatStore();

  // 历史会话栏 + 只读查看
  const [historySessions, setHistorySessions] = useState<Session[]>([]);
  const [viewing, setViewing] = useState<{ session: Session; messages: ChatMessage[] } | null>(null);
  // 左侧历史栏收起状态（持久化，记住用户偏好）
  const [historyCollapsed, setHistoryCollapsed] = useState(
    () => localStorage.getItem('callme_history_collapsed') === '1',
  );
  const toggleHistory = (collapsed: boolean) => {
    setHistoryCollapsed(collapsed);
    localStorage.setItem('callme_history_collapsed', collapsed ? '1' : '0');
  };

  const loadHistory = async () => {
    try {
      setHistorySessions(await api.listMySessions());
    } catch {
      /* 历史加载失败不打断主流程 */
    }
  };
  useEffect(() => {
    loadHistory();
  }, []);
  // 当前会话状态变化（新建/结束）时刷新历史列表
  useEffect(() => {
    loadHistory();
  }, [session?.id, session?.status, closedReason]);

  const openHistory = async (s: Session) => {
    if (session && s.id === session.id) {
      setViewing(null); // 点到当前会话 → 回到实时视图
      return;
    }
    try {
      const msgs = await api.listMessages(s.id);
      setViewing({ session: s, messages: msgs.map(toChatMessage) });
    } catch (err) {
      antMessage.error(apiErrorMessage(err));
    }
  };
  const backToLive = () => setViewing(null);

  const continueHistory = async (s: Session) => {
    try {
      setViewing(null);
      await continueSession(s.id);
      await loadHistory();
      antMessage.success('已基于历史会话继续问答');
    } catch (err) {
      antMessage.error(apiErrorMessage(err));
    }
  };

  const deleteHistory = async (s: Session) => {
    try {
      await api.deleteSessionHistory(s.id);
      if (viewing?.session.id === s.id) {
        setViewing(null);
      }
      await loadHistory();
      antMessage.success('历史会话已删除');
    } catch (err) {
      antMessage.error(apiErrorMessage(err));
    }
  };

  const [input, setInput] = useState('');
  const [images, setImages] = useState<ImageAttachment[]>([]);
  const [handoffOpen, setHandoffOpen] = useState(false);
  const [handoffReason, setHandoffReason] = useState('');
  const listRef = useRef<HTMLDivElement>(null);
  const [stickToBottom, setStickToBottom] = useState(true);
  const [showScrollBottom, setShowScrollBottom] = useState(false);

  useEffect(() => {
    restoreSession();
  }, [restoreSession]);

  const isNearBottom = useCallback((el: HTMLDivElement) => (
    el.scrollHeight - el.scrollTop - el.clientHeight <= AUTO_SCROLL_BOTTOM_THRESHOLD
  ), []);

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const el = listRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior });
  }, []);

  useEffect(() => {
    setStickToBottom(true);
    setShowScrollBottom(false);
    window.requestAnimationFrame(() => scrollToBottom('auto'));
  }, [session?.id, scrollToBottom]);

  useEffect(() => {
    if (!stickToBottom) return;
    window.requestAnimationFrame(() => scrollToBottom(thinking || busy ? 'auto' : 'smooth'));
  }, [messages, thinking, busy, stickToBottom, scrollToBottom]);

  const onMessagesScroll = useCallback(() => {
    const el = listRef.current;
    if (!el) return;
    const nearBottom = isNearBottom(el);
    setStickToBottom(nearBottom);
    setShowScrollBottom(!nearBottom);
  }, [isNearBottom]);

  const active = session?.status === 'active' && !closedReason;
  const queued = session?.status === 'queued' && !closedReason && position > 0;
  const connecting = session?.status === 'queued' && !closedReason && position <= 0;

  const fileToImageAttachment = useCallback((file: File): Promise<ImageAttachment> => {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = (e) => {
        const dataUrl = e.target?.result as string;
        const match = dataUrl.match(/^data:image\/[^;]+;base64,(.+)$/);
        if (!match) {
          reject(new Error('图片格式无效'));
          return;
        }
        const img = new window.Image();
        img.onload = () => {
          resolve({
            id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
            base64: match[1],
            mimeType: file.type,
            filename: file.name,
            width: img.width,
            height: img.height,
          });
        };
        img.onerror = () => reject(new Error('图片读取失败'));
        img.src = dataUrl;
      };
      reader.onerror = () => reject(new Error('图片读取失败'));
      reader.readAsDataURL(file);
    });
  }, []);

  const addImageFile = useCallback(async (file: File) => {
    if (!file.type.startsWith('image/')) {
      antMessage.warning('只能添加图片文件');
      return false;
    }
    if (file.size > MAX_IMAGE_SIZE) {
      antMessage.warning('单张图片不能超过 10MB');
      return false;
    }
    if (images.length >= MAX_IMAGES) {
      antMessage.warning(`最多添加 ${MAX_IMAGES} 张图片`);
      return false;
    }
    try {
      const attachment = await fileToImageAttachment(file);
      setImages((prev) => (prev.length >= MAX_IMAGES ? prev : [...prev, attachment]));
    } catch (err) {
      antMessage.error(apiErrorMessage(err));
    }
    return false;
  }, [fileToImageAttachment, images.length]);

  const removeImage = (id?: string) => {
    setImages((prev) => prev.filter((img) => img.id !== id));
  };

  const onPaste = useCallback(async (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData.items)
      .filter((item) => item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => !!file);
    for (const file of files) {
      await addImageFile(file);
    }
  }, [addImageFile]);

  const onDrop = useCallback(async (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    for (const file of Array.from(e.dataTransfer.files)) {
      if (file.type.startsWith('image/')) {
        await addImageFile(file);
      }
    }
  }, [addImageFile]);

  const onSend = () => {
    const content = input.trim();
    if ((!content && images.length === 0) || busy || !active) return;
    setStickToBottom(true);
    setShowScrollBottom(false);
    sendMessage(content, images.length > 0 ? images : undefined);
    setInput('');
    setImages([]);
  };

  const closeReasonText: Record<string, string> = {
    user: '会话已结束',
    idle: '由于长时间未活动，会话已自动结束',
    max_time: '会话已达最大时长，已自动结束',
    admin: '会话已被运营人员结束',
    error: '会话因异常已结束',
    queue_leave: '已离开排队',
  };

  // 左侧高亮项：查看历史时高亮该历史会话，否则高亮当前会话
  const activeHistoryId = viewing ? viewing.session.id : session?.id ?? null;

  return (
    <div className={`chat-layout ${historyCollapsed ? 'history-hidden' : ''}`}>
      {/* 左侧历史会话栏（可收起） */}
      {historyCollapsed ? (
        <Tooltip title="展开历史栏" placement="right">
          <Button
            className="history-expand-btn"
            type="text"
            icon={<MenuUnfoldOutlined />}
            onClick={() => toggleHistory(false)}
          />
        </Tooltip>
      ) : (
        <HistorySidebar
          sessions={historySessions}
          activeId={activeHistoryId}
          onSelect={openHistory}
          onContinue={continueHistory}
          onDelete={deleteHistory}
          onNew={() => {
            setViewing(null);
            if (!active && !queued && !connecting) startSession();
          }}
          onCollapse={() => toggleHistory(true)}
        />
      )}

      {/* 右侧对话区 */}
      <div className="chat-main">
        {viewing ? (
          // —— 历史会话只读视图 ——
          <>
            <div className="chat-main-head">
              <Space>
                <CustomerServiceOutlined style={{ fontSize: 20, color: 'var(--color-primary)' }} />
                <Title level={4} style={{ margin: 0 }}>{viewing.session.title || '历史会话'}</Title>
                <Tag>只读</Tag>
              </Space>
              <Button size="small" onClick={backToLive}>返回当前对话</Button>
            </div>
            <div className="chat-messages">
              {viewing.messages.length === 0 ? (
                <Empty description="该会话无消息" style={{ marginTop: 80 }} />
              ) : (
                viewing.messages.map((m, i) => (
                  <MessageBubble key={`${m.id}-${i}`} msg={m} sessionModel={m.model} readOnly />
                ))
              )}
            </div>
          </>
        ) : (
          // —— 实时对话视图 ——
          <>
            <div className="chat-main-head">
              <Space>
                <CustomerServiceOutlined style={{ fontSize: 20, color: 'var(--color-primary)' }} />
                <Title level={4} style={{ margin: 0 }}>智能问答</Title>
              </Space>
              {active && session?.startedAt && (
                <Space>
                  <SessionTimer startedAt={session.startedAt} />
                  <Button danger size="small" icon={<PoweroffOutlined />} onClick={endSession}>
                    结束会话
                  </Button>
                  <Button size="small" icon={<TeamOutlined />} onClick={() => setHandoffOpen(true)}>
                    转人工
                  </Button>
                </Space>
              )}
            </div>

            {idleWarning && active && (
              <Alert type="warning" showIcon closable style={{ marginBottom: 8 }}
                message="您已较长时间未发言，会话将在持续空闲后自动结束" />
            )}
            {error && <Alert type="error" showIcon closable style={{ marginBottom: 8 }} message={error} />}
            {closedReason && (
              <Alert type="info" showIcon style={{ marginBottom: 8 }}
                message={closeReasonText[closedReason] ?? '会话已结束'}
                action={<Button size="small" type="primary" onClick={startSession}>重新开始</Button>} />
            )}

            {/* 消息区 */}
            <div ref={listRef} className="chat-messages" onScroll={onMessagesScroll}>
              {!session && (
                <div style={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
                  <LogoIcon size={72} />
                  <Title level={3} style={{ margin: 0 }}>您好，我是 Callme 智能问题解决助手</Title>
                  <Text type="secondary">检索知识库 · 代码图谱 · 历史工单，帮你定位并解决研发与平台问题，越用越聪明</Text>
                  {restoring ? (
                    <Space>
                      <Spin size="small" />
                      <Text type="secondary">正在恢复会话…</Text>
                    </Space>
                  ) : (
                    <Button type="primary" size="large" onClick={startSession}>开始会话</Button>
                  )}
                </div>
              )}

              {queued && (
                <div style={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12 }}>
                  <div className="thinking-dots"><span /><span /><span /></div>
                  <Title level={4} style={{ margin: 0 }}>
                    当前有 {session?.active ?? 0} 人正在提问，最多支持 {session?.maxActive ?? 0} 人同时接入
                  </Title>
                  <Text type="secondary">当前排队 {session?.queueLen ?? queueLen} 人，您前方还有 {Math.max(0, position - 1)} 位</Text>
                  <Text type="secondary">接入后将立即为您服务</Text>
                  <Text type="secondary">进入时间 {session && dayjs(session.createdAt).format('HH:mm:ss')}</Text>
                  <Button onClick={endSession}>离开排队</Button>
                </div>
              )}

              {connecting && (
                <div style={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12 }}>
                  <div className="thinking-dots"><span /><span /><span /></div>
                  <Title level={4} style={{ margin: 0 }}>正在接入智能坐席</Title>
                  <Text type="secondary">正在启动 Agent，会话准备完成后即可开始提问</Text>
                  <Text type="secondary">进入时间 {session && dayjs(session.createdAt).format('HH:mm:ss')}</Text>
                  <Button onClick={endSession}>取消接入</Button>
                </div>
              )}

              {active && messages.length === 0 && (
                <Empty description="已接入，请描述你的问题（报错信息、环境、复现步骤越具体越好）" style={{ marginTop: 80 }} />
              )}

              {messages.map((m, i) => (
                <MessageBubble key={`${m.id}-${i}`} msg={m} sessionModel={session?.model} sessionAgentType={session?.agentType} />
              ))}
              {showScrollBottom && (
                <Tooltip title="回到底部">
                  <Button
                    className="scroll-bottom-btn"
                    shape="circle"
                    icon={<DownOutlined />}
                    onClick={() => {
                      setStickToBottom(true);
                      setShowScrollBottom(false);
                      scrollToBottom('smooth');
                    }}
                  />
                </Tooltip>
              )}
            </div>

            {/* 输入区 */}
            {active && (
              <div className="chat-input-wrap" onDrop={onDrop} onDragOver={(e) => e.preventDefault()}>
                {images.length > 0 && (
                  <div className="image-preview-strip">
                    {images.map((img, index) => (
                      <div key={img.id ?? index} className="image-preview-item">
                        <Image
                          src={`data:${img.mimeType};base64,${img.base64}`}
                          alt={img.filename || `图片 ${index + 1}`}
                          width={72}
                          height={72}
                          style={{ objectFit: 'cover', borderRadius: 8 }}
                        />
                        <Button
                          type="text"
                          size="small"
                          icon={<CloseOutlined />}
                          className="image-preview-remove"
                          onClick={() => removeImage(img.id)}
                        />
                      </div>
                    ))}
                  </div>
                )}
                <div style={{ display: 'flex', gap: 8 }}>
                  <div style={{ position: 'relative', flex: 1, minWidth: 0 }}>
                    <Input.TextArea
                      autoSize={{ minRows: 1, maxRows: 4 }}
                      placeholder={busy ? '回答生成中…' : '输入您的问题，Enter 发送，Shift+Enter 换行，支持粘贴/拖拽图片'}
                      value={input}
                      disabled={busy}
                      onChange={(e) => setInput(e.target.value)}
                      onPaste={onPaste}
                      onPressEnter={(e) => {
                        if (!e.shiftKey) {
                          e.preventDefault();
                          onSend();
                        }
                      }}
                    />
                    <Upload
                      accept="image/*"
                      showUploadList={false}
                      beforeUpload={addImageFile}
                      multiple
                    >
                      <Button
                        type="text"
                        icon={<PictureOutlined />}
                        disabled={busy || images.length >= MAX_IMAGES}
                        className="image-upload-button"
                      />
                    </Upload>
                  </div>
                  <Button
                    type="primary"
                    icon={<SendOutlined />}
                    disabled={busy || (!input.trim() && images.length === 0)}
                    onClick={onSend}
                  >
                    发送
                  </Button>
                  {busy && (
                    <Button danger icon={<StopOutlined />} onClick={endSession}>
                      停止
                    </Button>
                  )}
                </div>
              </div>
            )}
          </>
        )}
      </div>

      {/* 转人工弹窗 */}
      <Modal
        title="转人工服务"
        open={handoffOpen}
        okText="提交"
        cancelText="取消"
        onCancel={() => setHandoffOpen(false)}
        onOk={async () => {
          try {
            const ticketId = await requestHandoff(handoffReason || '用户主动转人工');
            setHandoffOpen(false);
            setHandoffReason('');
            antMessage.success(`已生成工单 ${ticketId.slice(0, 8)}，人工专家将尽快跟进`);
          } catch (err) {
            antMessage.error(apiErrorMessage(err));
          }
        }}
      >
        <Text type="secondary">将把当前会话的完整上下文（含已排查信息）转交人工专家处理。</Text>
        <Input.TextArea
          rows={3}
          style={{ marginTop: 12 }}
          placeholder="补充说明问题与已尝试的排查（可选）"
          value={handoffReason}
          onChange={(e) => setHandoffReason(e.target.value)}
        />
      </Modal>
    </div>
  );
}
