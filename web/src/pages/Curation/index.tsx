// 管理员「知识沉淀」审批页（自学习沙箱）
// 反馈蒸馏出的候选资产在此审批：查看来源证据 → 编辑 → 通过(发布为正式知识)/拒绝。
// 任何候选在通过前都不会进入生产回答链路。
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Empty,
  Image,
  Input,
  Modal,
  Popconfirm,
  Segmented,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
  message,
} from 'antd';
import { CheckOutlined, CloseOutlined, EditOutlined, PictureOutlined, ReloadOutlined } from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import { useAuthStore } from '../../store/authStore';
import { useAITaskStore } from '../../store/aiTaskStore';
import { formatAITaskContent } from '../../utils/aiTaskFormat';
import type {
  CandidateAsset,
  CandidateAssetStatus,
  RuntimeLearningAsset,
  RuntimeLearningStatus,
  ImageAttachment,
  KnowledgePublishTarget,
  LearningJob,
  LearningJobStatus,
  AgentCapabilities,
} from '../../types';

const { Title, Text, Paragraph } = Typography;
const MAX_MANUAL_IMAGES = 5;
const MAX_MANUAL_IMAGE_SIZE = 10 * 1024 * 1024;

const statusMeta: Record<CandidateAssetStatus, { label: string; color: string }> = {
  pending: { label: '待审批', color: 'gold' },
  approved: { label: '已通过', color: 'green' },
  rejected: { label: '已拒绝', color: 'default' },
};

const runtimeStatusMeta: Record<RuntimeLearningStatus, { label: string; color: string }> = {
  pending_review: { label: '待审计', color: 'gold' },
  kept: { label: '已保留', color: 'green' },
  modified: { label: '已修改', color: 'blue' },
  deleted: { label: '已删除', color: 'red' },
  converted: { label: '已转候选', color: 'cyan' },
  prohibited_as_evidence: { label: '禁作依据', color: 'volcano' },
};

const jobStatusMeta: Record<LearningJobStatus, { label: string; color: string }> = {
  running: { label: '运行中', color: 'processing' },
  succeeded: { label: '成功', color: 'green' },
  failed: { label: '失败', color: 'red' },
  skipped: { label: '跳过', color: 'default' },
};

const publishTargetMeta: Record<KnowledgePublishTarget, { label: string; color: string; description: string }> = {
  local: { label: '本地知识', color: 'green', description: '写入 approved_knowledge.md，作为正式知识依据' },
  skill: { label: 'Skill', color: 'purple', description: '写入当前 Agent Runtime 的 Skill 目录' },
  knowledge_base: { label: '知识库', color: 'blue', description: '外部知识库写入器待接入' },
};

const publishTargetOptions = [
  { label: '本地知识', value: 'local' as KnowledgePublishTarget },
  { label: 'Skill', value: 'skill' as KnowledgePublishTarget },
  { label: '写入知识库（待接入）', value: 'knowledge_base' as KnowledgePublishTarget, disabled: true },
];

type RuntimeReviewAction = 'keep' | 'delete' | 'modify';

function normalizePublishTargets(targets?: KnowledgePublishTarget[]): KnowledgePublishTarget[] {
  return targets?.length ? targets : ['local'];
}

function renderPublishTargets(targets?: KnowledgePublishTarget[]) {
  return normalizePublishTargets(targets).map((target) => {
    const meta = publishTargetMeta[target] ?? publishTargetMeta.local;
    return <Tag key={target} color={meta.color}>{meta.label}</Tag>;
  });
}

function parseEvidence(raw?: string): Record<string, unknown> | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function formatRuntimeAssetContent(row: RuntimeLearningAsset): string {
  const content = row.content?.trim();
  if (!content) return '（删除记录或内容为空）';
  if (row.assetType === 'skill') {
    return content;
  }
  return content;
}

export default function CurationPage() {
  const { user, activeRole } = useAuthStore();
  const roles = user?.roles?.length ? user.roles : user ? [user.role] : [];
  const usingRole = activeRole && roles.includes(activeRole as typeof roles[number]) ? activeRole : user?.role;
  const canReview = usingRole === 'admin' || usingRole === 'knowledge_expert';
  const { startTask, appendTask, setTaskContent, finishTask, failTask } = useAITaskStore();
  const [track, setTrack] = useState<'candidates' | 'approved' | 'runtime'>('candidates');
  const [candidateView, setCandidateView] = useState<'manual' | 'ai' | 'review' | 'jobs'>('manual');
  const [status, setStatus] = useState<CandidateAssetStatus>('pending');
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeLearningStatus>('pending_review');
  const [items, setItems] = useState<CandidateAsset[]>([]);
  const [runtimeItems, setRuntimeItems] = useState<RuntimeLearningAsset[]>([]);
  const [jobs, setJobs] = useState<LearningJob[]>([]);
  const [approvedNotes, setApprovedNotes] = useState('');
  const [loading, setLoading] = useState(false);
  const [learningStarting, setLearningStarting] = useState(false);
  const [reviewingRuntimeID, setReviewingRuntimeID] = useState<string | null>(null);
  const [viewingRuntime, setViewingRuntime] = useState<RuntimeLearningAsset | null>(null);
  const [runtimeDraftContent, setRuntimeDraftContent] = useState('');
  const [runtimeAIInstruction, setRuntimeAIInstruction] = useState('');
  const [runtimeAILoading, setRuntimeAILoading] = useState(false);
  const [editing, setEditing] = useState<CandidateAsset | null>(null);
  const [viewing, setViewing] = useState<CandidateAsset | null>(null);
  const [rejecting, setRejecting] = useState<CandidateAsset | null>(null);
  const [rejectNote, setRejectNote] = useState('');
  const [manualTargets, setManualTargets] = useState<KnowledgePublishTarget[]>(['local']);
  const [manualDescription, setManualDescription] = useState('');
  const [manualImages, setManualImages] = useState<ImageAttachment[]>([]);
  const [manualStreaming, setManualStreaming] = useState(false);
  const [manualStreamContent, setManualStreamContent] = useState('');
  const [agentCapabilities, setAgentCapabilities] = useState<AgentCapabilities | null>(null);
  const runtimeEditorRef = useRef<HTMLTextAreaElement | null>(null);
  const runtimePreviewRef = useRef<HTMLDivElement | null>(null);

  const syncRuntimePreviewScroll = useCallback(() => {
    const editor = runtimeEditorRef.current;
    const preview = runtimePreviewRef.current;
    if (!editor || !preview) return;
    const maxFrom = editor.scrollHeight - editor.clientHeight;
    const maxTo = preview.scrollHeight - preview.clientHeight;
    if (maxFrom <= 0 || maxTo <= 0) return;
    preview.scrollTop = (editor.scrollTop / maxFrom) * maxTo;
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      if (track === 'candidates' && (candidateView === 'manual' || candidateView === 'ai')) {
        return;
      }
      if (track === 'candidates' && candidateView === 'review') {
        setItems(await api.listCandidates(status));
      } else if (track === 'runtime') {
        setRuntimeItems(await api.listRuntimeLearningAssets(runtimeStatus));
      } else if (track === 'candidates' && candidateView === 'jobs') {
        setJobs(await api.listLearningJobs());
      } else {
        setApprovedNotes(await api.getLearningNotes());
      }
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [track, candidateView, status, runtimeStatus]);

  useEffect(() => {
    if (!canReview && track === 'runtime') {
      setTrack('candidates');
      return;
    }
    load();
  }, [canReview, candidateView, load, track]);

  useEffect(() => {
    api.getAgentCapabilities()
      .then(setAgentCapabilities)
      .catch(() => setAgentCapabilities(null));
  }, []);

  useEffect(() => {
    if (track !== 'candidates' || candidateView !== 'jobs') return;
    if (!jobs.some((job) => job.status === 'running')) return;
    const timer = window.setInterval(async () => {
      try {
        setJobs(await api.listLearningJobs());
      } catch (err) {
        message.error(apiErrorMessage(err));
      }
    }, 2000);
    return () => window.clearInterval(timer);
  }, [track, candidateView, jobs]);

  useEffect(() => {
    if (!viewingRuntime) {
      setRuntimeDraftContent('');
      setRuntimeAIInstruction('');
      return;
    }
    setRuntimeDraftContent(formatRuntimeAssetContent(viewingRuntime));
    setRuntimeAIInstruction('');
  }, [viewingRuntime]);

  const approve = async (c: CandidateAsset) => {
    try {
      await api.reviewCandidate(c.id, true);
      message.success('已通过并发布为正式知识');
      load();
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const doReject = async () => {
    if (!rejecting) return;
    try {
      await api.reviewCandidate(rejecting.id, false, rejectNote);
      message.success('已拒绝');
      setRejecting(null);
      setRejectNote('');
      load();
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const runLearningJob = async () => {
    setLearningStarting(true);
    try {
      const job = await api.runLearningJob();
      message.success('AI 学习任务已开始');
      setTrack('candidates');
      setCandidateView('jobs');
      setJobs((prev) => [job, ...prev.filter((item) => item.id !== job.id)]);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLearningStarting(false);
    }
  };

  const reviewRuntimeAsset = async (row: RuntimeLearningAsset, action: RuntimeReviewAction) => {
    const actionLabels: Record<RuntimeReviewAction, string> = {
      keep: '保留',
      delete: '删除',
      modify: '保存修改',
    };
    setReviewingRuntimeID(row.id);
    try {
      await api.reviewRuntimeLearningAsset(row.id, action, undefined, action === 'modify' ? runtimeDraftContent : undefined);
      message.success(`已${actionLabels[action]}`);
      setRuntimeItems((prev) => prev.filter((item) => item.id !== row.id));
      setViewingRuntime(null);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setReviewingRuntimeID(null);
    }
  };

  const assistRuntimeEdit = async () => {
    if (!viewingRuntime) return;
    if (!runtimeAIInstruction.trim()) {
      message.warning('请先输入希望 AI 修改的要求');
      return;
    }
    const draftBeforeEdit = runtimeDraftContent.trim();
    if (!draftBeforeEdit) {
      message.warning('当前审计内容为空，无法生成修订稿。可以先补充内容，或直接删除这条空文件记录。');
      return;
    }
    setRuntimeAILoading(true);
    const taskId = startTask({ title: 'AI 修订 Runtime 内容', source: '知识沉淀 / Runtime 审计' });
    setRuntimeDraftContent('');
    try {
      await api.streamRuntimeLearningEdit(viewingRuntime.id, runtimeAIInstruction.trim(), draftBeforeEdit, (event) => {
        if (event.type === 'status') {
          setTaskContent(taskId, event.content ?? '');
        }
        if (event.type === 'delta') {
          const delta = event.delta ?? '';
          setRuntimeDraftContent((prev) => prev + delta);
          appendTask(taskId, delta);
        }
        if (event.type === 'done') {
          setRuntimeDraftContent(event.content ?? '');
          setTaskContent(taskId, event.content ?? '');
          finishTask(taskId);
        }
      });
      message.success('AI 已生成修订稿，请确认后再保存');
    } catch (err) {
      const msg = apiErrorMessage(err);
      failTask(taskId, msg);
      message.error(msg);
    } finally {
      setRuntimeAILoading(false);
    }
  };

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

  const addManualImage = useCallback(async (file: File) => {
    if (agentCapabilities?.supportsMultimodal !== true) {
      message.warning(`当前启用模型 ${agentCapabilities?.defaultModel || '未配置'} 不支持图片证据，请切换到支持多模态的模型后再上传`);
      return false;
    }
    if (!file.type.startsWith('image/')) {
      message.warning('只能添加图片文件');
      return false;
    }
    if (file.size > MAX_MANUAL_IMAGE_SIZE) {
      message.warning('单张图片不能超过 10MB');
      return false;
    }
    if (manualImages.length >= MAX_MANUAL_IMAGES) {
      message.warning(`最多添加 ${MAX_MANUAL_IMAGES} 张图片`);
      return false;
    }
    try {
      const img = await fileToImageAttachment(file);
      setManualImages((prev) => (prev.length >= MAX_MANUAL_IMAGES ? prev : [...prev, img]));
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
    return false;
  }, [agentCapabilities, fileToImageAttachment, manualImages.length]);

  const createManualDraft = async () => {
    if (!manualDescription.trim() && manualImages.length === 0) {
      message.warning('请先输入知识描述或上传图片');
      return;
    }
    if (manualImages.length > 0 && agentCapabilities?.supportsMultimodal !== true) {
      message.warning(`当前启用模型 ${agentCapabilities?.defaultModel || '未配置'} 不支持图片证据，请移除图片或切换模型`);
      return;
    }
    setManualStreaming(true);
    setManualStreamContent('');
    const taskId = startTask({ title: '生成候选知识', source: '知识沉淀 / 人工录入' });
    try {
      await api.streamManualKnowledgeDraft({
        publishTargets: manualTargets,
        description: manualDescription.trim(),
        images: manualImages,
      }, (event) => {
        if (event.type === 'status') {
          setManualStreamContent(event.content ?? '');
          setTaskContent(taskId, event.content ?? '');
        }
        if (event.type === 'delta') {
          const delta = event.delta ?? '';
          setManualStreamContent((prev) => prev + delta);
          appendTask(taskId, delta);
        }
        if (event.type === 'done') {
          setManualStreamContent(event.content ?? '');
          setTaskContent(taskId, event.content ?? '');
          finishTask(taskId);
        }
      });
      message.success('已生成候选知识，等待审批');
      setManualDescription('');
      setManualImages([]);
      setManualStreamContent('');
      setStatus('pending');
      setTrack('candidates');
      setCandidateView('review');
    } catch (err) {
      const msg = apiErrorMessage(err);
      failTask(taskId, msg);
      message.error(msg);
    } finally {
      setManualStreaming(false);
    }
  };

  const saveEdit = async () => {
    if (!editing) return;
    try {
      await api.updateCandidate(editing.id, {
        assetType: editing.assetType,
        publishTargets: normalizePublishTargets(editing.publishTargets),
        title: editing.title,
        question: editing.question,
        content: editing.content,
      });
      message.success('已保存');
      setEditing(null);
      load();
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 1100, margin: '0 auto' }}>
      <Space style={{ marginBottom: 8, width: '100%', justifyContent: 'space-between' }}>
        <Title level={4} style={{ margin: 0 }}>知识沉淀</Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
        </Space>
      </Space>
      <Paragraph type="secondary" style={{ fontSize: 13 }}>
        {canReview ? '知识候选统一进入审批流；Agent 自学习单独审计纠偏。' : '知识专员可录入并维护候选知识，提交后由知识专家或管理员审批。'}
        <Text strong>业务事实必须审批后才可作为正式依据</Text>。
      </Paragraph>

      <Segmented
        style={{ marginBottom: 12 }}
        value={track}
        onChange={(v) => setTrack(v as 'candidates' | 'approved' | 'runtime')}
        options={[
          { label: '候选知识', value: 'candidates' },
          { label: '正式知识', value: 'approved' },
          ...(canReview ? [
            { label: 'Agent 自学习审计', value: 'runtime' },
          ] : []),
        ]}
      />

      {track === 'candidates' && (
        <Segmented
          style={{ marginBottom: 12, display: 'block' }}
          value={candidateView}
          onChange={(v) => setCandidateView(v as 'manual' | 'ai' | 'review' | 'jobs')}
          options={[
            { label: '人工录入', value: 'manual' },
            { label: 'AI 挖掘', value: 'ai' },
            { label: '候选审批', value: 'review' },
            { label: '执行历史', value: 'jobs' },
          ]}
        />
      )}

      {track === 'candidates' && candidateView === 'review' && (
        <Segmented
          style={{ marginBottom: 16, display: 'block' }}
          value={status}
          onChange={(v) => setStatus(v as CandidateAssetStatus)}
          options={[
            { label: '待审批', value: 'pending' },
            { label: '已通过', value: 'approved' },
            { label: '已拒绝', value: 'rejected' },
          ]}
        />
      )}
      {track === 'runtime' && (
        <Segmented
          style={{ marginBottom: 16, display: 'block' }}
          value={runtimeStatus}
          onChange={(v) => setRuntimeStatus(v as RuntimeLearningStatus)}
        options={[
          { label: '待审计', value: 'pending_review' },
          { label: '已保留', value: 'kept' },
          { label: '已修改', value: 'modified' },
          { label: '已删除', value: 'deleted' },
        ]}
        />
      )}

      <Card>
        {track === 'candidates' && candidateView === 'manual' && (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            {agentCapabilities?.supportsMultimodal !== true && (
              <Alert
                type="info"
                showIcon
                message="当前启用模型不支持图片证据，人工录入可继续使用文字描述"
              />
            )}
            <Space direction="vertical" size={6} style={{ width: '100%' }}>
              <Text strong>发布目标</Text>
              <Checkbox.Group
                value={manualTargets}
                options={publishTargetOptions}
                onChange={(values) => setManualTargets(values as KnowledgePublishTarget[])}
              />
              <Text type="secondary" style={{ fontSize: 12 }}>
                生成候选知识后，审批通过时会按发布目标写入。外部知识库写入器后续接入后可启用。
              </Text>
            </Space>
            <Space direction="vertical" size={6} style={{ width: '100%' }}>
              <Text strong>原始描述</Text>
              <Input.TextArea
                rows={8}
                value={manualDescription}
                onChange={(e) => setManualDescription(e.target.value)}
                placeholder="描述需要沉淀的知识、适用场景、处理步骤、注意事项；也可以粘贴截图后补充说明。"
              />
            </Space>
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              <Space>
                <Text strong>图片证据</Text>
                <Upload
                  accept="image/*"
                  showUploadList={false}
                  beforeUpload={addManualImage}
                  multiple
                >
                  <Button
                    icon={<PictureOutlined />}
                    disabled={loading || agentCapabilities?.supportsMultimodal !== true || manualImages.length >= MAX_MANUAL_IMAGES}
                  >
                    添加图片
                  </Button>
                </Upload>
              </Space>
              {manualImages.length > 0 && (
                <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                  {manualImages.map((img, index) => (
                    <div key={img.id ?? index} style={{ position: 'relative' }}>
                      <Image
                        src={`data:${img.mimeType};base64,${img.base64}`}
                        alt={img.filename || `图片 ${index + 1}`}
                        width={96}
                        height={96}
                        style={{ objectFit: 'cover', borderRadius: 6 }}
                      />
                      <Button
                        type="text"
                        size="small"
                        icon={<CloseOutlined />}
                        onClick={() => setManualImages((prev) => prev.filter((item) => item.id !== img.id))}
                        style={{ position: 'absolute', top: 0, right: 0, background: 'rgba(0,0,0,0.45)', color: '#fff' }}
                      />
                    </div>
                  ))}
                </div>
              )}
            </Space>
            <Space>
              <Button type="primary" onClick={createManualDraft} loading={manualStreaming}>
                {manualStreaming ? '生成中' : '生成候选知识'}
              </Button>
              <Button onClick={() => {
                setManualDescription('');
                setManualImages([]);
                setManualStreamContent('');
              }}>
                清空
              </Button>
            </Space>
            {(manualStreaming || manualStreamContent) && (
              <Card size="small" title="AI 生成过程">
                <div className={`runtime-asset-preview markdown-body ${manualStreaming ? 'streaming-cursor' : ''}`}>
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>
                    {manualStreamContent ? formatAITaskContent(manualStreamContent) : '正在连接 AI，请稍候…'}
                  </ReactMarkdown>
                </div>
              </Card>
            )}
          </Space>
        )}
        {track === 'candidates' && candidateView === 'ai' && (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <div>
              <Title level={5} style={{ marginTop: 0 }}>AI 挖掘历史会话</Title>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>
                AI 会从最近已结束的会话中提炼可复用知识，生成候选知识后仍需人工审批；不会直接进入正式知识或 Agent 回答链路。
              </Paragraph>
            </div>
            <Space>
              <Button type="primary" onClick={runLearningJob} loading={learningStarting}>
                立即开始挖掘
              </Button>
              <Button onClick={() => {
                setCandidateView('jobs');
                setTrack('candidates');
              }}>
                查看执行历史
              </Button>
            </Space>
          </Space>
        )}
        {track === 'candidates' && candidateView === 'review' && (items.length === 0 ? (
          <Empty description="暂无候选知识" />
        ) : (
          <Table<CandidateAsset>
            rowKey="id"
            dataSource={items}
            loading={loading}
            pagination={{ pageSize: 10 }}
            columns={[
              {
                title: '发布目标',
                dataIndex: 'publishTargets',
                width: 180,
                render: (targets: KnowledgePublishTarget[]) => renderPublishTargets(targets),
              },
              { title: '标题', dataIndex: 'title', ellipsis: true },
              {
                title: '来源会话',
                dataIndex: 'sourceSessionId',
                width: 100,
                render: (id: string) => (id ? <Text type="secondary">{id.slice(0, 8)}</Text> : '-'),
              },
              {
                title: '置信度',
                dataIndex: 'confidence',
                width: 80,
                render: (v: number) => `${Math.round(v * 100)}%`,
              },
              {
                title: '状态',
                dataIndex: 'status',
                width: 90,
                render: (s: CandidateAssetStatus) => <Tag color={statusMeta[s].color}>{statusMeta[s].label}</Tag>,
              },
              {
                title: '时间',
                dataIndex: 'createdAt',
                width: 130,
                render: (t: string) => dayjs(t).format('MM-DD HH:mm'),
              },
              {
                title: '操作',
                width: 230,
                render: (_, row) => (
                  <Space size={4}>
                    <Button size="small" onClick={() => setViewing(row)}>证据</Button>
                    {row.status === 'pending' && (
                      <>
                        <Tooltip title="编辑后再审批">
                          <Button size="small" icon={<EditOutlined />} onClick={() => setEditing({ ...row })} />
                        </Tooltip>
                        {canReview && (
                          <>
                            <Button size="small" type="primary" icon={<CheckOutlined />} onClick={() => approve(row)}>
                              通过
                            </Button>
                            <Button size="small" danger icon={<CloseOutlined />} onClick={() => setRejecting(row)}>
                              拒绝
                            </Button>
                          </>
                        )}
                      </>
                    )}
                  </Space>
                ),
              },
            ]}
          />
        ))}
        {track === 'runtime' && (runtimeItems.length === 0 ? (
          <Empty description="暂无 Agent 自学习审计记录" />
        ) : (
          <Table<RuntimeLearningAsset>
            rowKey="id"
            dataSource={runtimeItems}
            loading={loading}
            pagination={{ pageSize: 10 }}
            columns={[
              {
                title: '类型',
                dataIndex: 'assetType',
                width: 80,
                render: (t: string) => <Tag color={t === 'skill' ? 'purple' : 'blue'}>{t}</Tag>,
              },
              {
                title: '变更',
                dataIndex: 'changeType',
                width: 90,
                render: (t: string) => {
                  const color = t === 'new' ? 'green' : t === 'modified' ? 'orange' : 'red';
                  return <Tag color={color}>{t}</Tag>;
                },
              },
              {
                title: '路径',
                dataIndex: 'path',
                ellipsis: true,
                render: (p: string) => <Text code>{p}</Text>,
              },
              {
                title: '状态',
                dataIndex: 'status',
                width: 110,
                render: (s: RuntimeLearningStatus) => <Tag color={runtimeStatusMeta[s].color}>{runtimeStatusMeta[s].label}</Tag>,
              },
              {
                title: '时间',
                dataIndex: 'createdAt',
                width: 130,
                render: (t: string) => dayjs(t).format('MM-DD HH:mm'),
              },
              {
                title: '操作',
                width: 110,
                render: (_, row) => (
                  <Button size="small" onClick={() => setViewingRuntime(row)}>
                    查看处理
                  </Button>
                ),
              },
            ]}
          />
        ))}
        {track === 'candidates' && candidateView === 'jobs' && (jobs.length === 0 ? (
          <Empty description="暂无 AI 学习任务记录" />
        ) : (
          <Table<LearningJob>
            rowKey="id"
            dataSource={jobs}
            loading={loading}
            pagination={{ pageSize: 10 }}
            columns={[
              { title: '任务', dataIndex: 'id', width: 100, render: (id: string) => <Text code>{id.slice(0, 8)}</Text> },
              { title: '来源', dataIndex: 'source', width: 90, render: (s: string) => <Tag>{s}</Tag> },
              {
                title: '状态',
                dataIndex: 'status',
                width: 90,
                render: (s: LearningJobStatus) => <Tag color={jobStatusMeta[s].color}>{jobStatusMeta[s].label}</Tag>,
              },
              { title: '输入会话', dataIndex: 'inputSessions', width: 90 },
              { title: '生成候选', dataIndex: 'outputAssets', width: 90 },
              {
                title: '开始时间',
                dataIndex: 'startedAt',
                width: 140,
                render: (t: string) => dayjs(t).format('MM-DD HH:mm:ss'),
              },
              {
                title: '结束时间',
                dataIndex: 'finishedAt',
                width: 140,
                render: (t?: string) => (t ? dayjs(t).format('MM-DD HH:mm:ss') : '-'),
              },
              { title: '说明', dataIndex: 'error', ellipsis: true, render: (e?: string) => e || '-' },
            ]}
          />
        ))}
        {track === 'approved' && (
          <Card
            size="small"
            title="正式知识（approved_knowledge.md）"
            bordered={false}
          >
            <Paragraph type="secondary">
              这里展示已人工审批发布的正式客服知识。候选知识和 Agent 自学习审计记录在通过前不会自动进入这里。
            </Paragraph>
            <pre style={{ whiteSpace: 'pre-wrap', maxHeight: 560, overflow: 'auto', fontSize: 13, margin: 0 }}>
              {approvedNotes || '（暂无正式知识；候选知识通过人工审批后会发布到这里）'}
            </pre>
          </Card>
        )}
      </Card>

      <Modal
        title={viewingRuntime?.assetType === 'skill' ? 'Skill 审计处理' : 'Memory 审计处理'}
        open={!!viewingRuntime}
        width={900}
        onCancel={() => setViewingRuntime(null)}
        footer={viewingRuntime ? (
          <Space wrap>
            <Button onClick={() => setViewingRuntime(null)}>关闭</Button>
            {viewingRuntime.status === 'pending_review' && (
              <>
                <Button
                  type="primary"
                  loading={reviewingRuntimeID === viewingRuntime.id}
                  onClick={() => reviewRuntimeAsset(viewingRuntime, 'keep')}
                >
                  保留
                </Button>
                <Button
                  loading={reviewingRuntimeID === viewingRuntime.id}
                  onClick={() => reviewRuntimeAsset(viewingRuntime, 'modify')}
                >
                  保存修改并生效
                </Button>
                <Popconfirm
                  title="删除 Agent 自学习文件？"
                  description="会删除磁盘上的对应文件，并把该审计记录标记为已删除。"
                  okText="删除"
                  cancelText="取消"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => reviewRuntimeAsset(viewingRuntime, 'delete')}
                >
                  <Button danger loading={reviewingRuntimeID === viewingRuntime.id}>
                    删除文件
                  </Button>
                </Popconfirm>
              </>
            )}
          </Space>
        ) : null}
      >
        {viewingRuntime && (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <Space wrap>
              <Tag color={viewingRuntime.assetType === 'skill' ? 'purple' : 'blue'}>{viewingRuntime.assetType}</Tag>
              <Tag>{viewingRuntime.changeType}</Tag>
              <Tag color={runtimeStatusMeta[viewingRuntime.status]?.color}>
                {runtimeStatusMeta[viewingRuntime.status]?.label ?? viewingRuntime.status}
              </Tag>
            </Space>
            <Text code copyable style={{ whiteSpace: 'normal' }}>{viewingRuntime.path}</Text>
            {viewingRuntime.status === 'pending_review' && (
              <Card size="small" title="AI 辅助修改">
                <Space direction="vertical" style={{ width: '100%' }} size={8}>
                  <Input.TextArea
                    rows={2}
                    value={runtimeAIInstruction}
                    onChange={(e) => setRuntimeAIInstruction(e.target.value)}
                    placeholder="描述希望 AI 如何修改，例如：删除不确定的业务结论，补充适用范围，把语气改成客服可用的步骤说明"
                  />
                  <Button loading={runtimeAILoading} onClick={assistRuntimeEdit}>
                    AI 生成修订稿
                  </Button>
                </Space>
              </Card>
            )}
            <div className="runtime-review-split">
              <Card size="small" title="编写稿" className="runtime-review-pane">
                <Input.TextArea
                  ref={runtimeEditorRef}
                  value={runtimeDraftContent}
                  onChange={(e) => setRuntimeDraftContent(e.target.value)}
                  onScroll={syncRuntimePreviewScroll}
                  disabled={viewingRuntime.status !== 'pending_review'}
                  className="runtime-review-editor"
                />
              </Card>
              <Card size="small" title="预览" className="runtime-review-pane">
                <div
                  ref={runtimePreviewRef}
                  className="runtime-asset-preview markdown-body runtime-review-preview"
                >
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{runtimeDraftContent}</ReactMarkdown>
                </div>
              </Card>
            </div>
            {viewingRuntime.status === 'pending_review' && (
              <Text type="secondary">
                可以人工修改，也可以让 AI 生成修订稿；只有点击“保存修改并生效”才会写回 Runtime 文件。
              </Text>
            )}
            {viewingRuntime.reviewNote && <Text type="secondary">审计备注：{viewingRuntime.reviewNote}</Text>}
          </Space>
        )}
      </Modal>

      {/* 证据/详情 */}
      <Modal
        title="候选知识 · 来源证据"
        open={!!viewing}
        footer={null}
        width={680}
        onCancel={() => setViewing(null)}
      >
        {viewing && (
          <Space direction="vertical" style={{ width: '100%' }} size={8}>
            <Text strong>{viewing.title}</Text>
            <Space wrap>{renderPublishTargets(viewing.publishTargets)}</Space>
            {viewing.question && <Text type="secondary">问题：{viewing.question}</Text>}
            <Card size="small" title="答案 / 内容">
              <Paragraph style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{viewing.content}</Paragraph>
            </Card>
            <Card size="small" title="来源证据（可追溯）">
              {(() => {
                const ev = parseEvidence(viewing.evidence);
                if (!ev) return <Text type="secondary">无</Text>;
                return (
                  <pre style={{ whiteSpace: 'pre-wrap', margin: 0, fontSize: 12 }}>
                    {JSON.stringify(ev, null, 2)}
                  </pre>
                );
              })()}
            </Card>
            {viewing.reviewNote && <Text type="secondary">审批备注：{viewing.reviewNote}</Text>}
          </Space>
        )}
      </Modal>

      {/* 编辑 */}
      <Modal
        title="编辑候选知识"
        open={!!editing}
        okText="保存"
        cancelText="取消"
        onOk={saveEdit}
        onCancel={() => setEditing(null)}
        width={680}
      >
        {editing && (
          <Space direction="vertical" style={{ width: '100%' }} size={12}>
            <div>
              <Text type="secondary">发布目标</Text>
              <br />
              <Checkbox.Group
                value={normalizePublishTargets(editing.publishTargets)}
                options={publishTargetOptions}
                onChange={(values) => setEditing({ ...editing, publishTargets: values as KnowledgePublishTarget[] })}
              />
            </div>
            <div>
              <Text type="secondary">标题</Text>
              <Input value={editing.title} onChange={(e) => setEditing({ ...editing, title: e.target.value })} />
            </div>
            <div>
              <Text type="secondary">相关问题（可空）</Text>
              <Input
                value={editing.question}
                onChange={(e) => setEditing({ ...editing, question: e.target.value })}
              />
            </div>
            <div>
              <Text type="secondary">答案 / 内容</Text>
              <Input.TextArea
                rows={6}
                value={editing.content}
                onChange={(e) => setEditing({ ...editing, content: e.target.value })}
              />
            </div>
          </Space>
        )}
      </Modal>

      {/* 拒绝 */}
      <Modal
        title="拒绝候选知识"
        open={!!rejecting}
        okText="确认拒绝"
        okButtonProps={{ danger: true }}
        cancelText="取消"
        onOk={doReject}
        onCancel={() => {
          setRejecting(null);
          setRejectNote('');
        }}
      >
        <Text type="secondary">可填写拒绝原因（可选）：</Text>
        <Input.TextArea
          rows={3}
          style={{ marginTop: 8 }}
          value={rejectNote}
          onChange={(e) => setRejectNote(e.target.value)}
          placeholder="如：证据不足 / 过度泛化 / 与现有知识冲突…"
        />
      </Modal>
    </div>
  );
}
