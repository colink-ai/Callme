// 管理员「知识沉淀」审批页（自学习沙箱）
// 反馈蒸馏出的候选资产在此审批：查看来源证据 → 编辑 → 通过(发布为正式知识)/拒绝。
// 任何候选在通过前都不会进入生产回答链路。
import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Image,
  Input,
  Modal,
  Segmented,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
  message,
} from 'antd';
import { CheckOutlined, CloseOutlined, EditOutlined, PictureOutlined, ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import { useAuthStore } from '../../store/authStore';
import type {
  CandidateAsset,
  CandidateAssetStatus,
  CandidateAssetType,
  HermesLearningAsset,
  HermesLearningStatus,
  ImageAttachment,
  LearningJob,
  LearningJobStatus,
} from '../../types';

const { Title, Text, Paragraph } = Typography;
const MAX_MANUAL_IMAGES = 5;
const MAX_MANUAL_IMAGE_SIZE = 10 * 1024 * 1024;

const statusMeta: Record<CandidateAssetStatus, { label: string; color: string }> = {
  pending: { label: '待审批', color: 'gold' },
  approved: { label: '已通过', color: 'green' },
  rejected: { label: '已拒绝', color: 'default' },
};

const hermesStatusMeta: Record<HermesLearningStatus, { label: string; color: string }> = {
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

function parseEvidence(raw?: string): Record<string, unknown> | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

export default function CurationPage() {
  const { user, activeRole } = useAuthStore();
  const roles = user?.roles?.length ? user.roles : user ? [user.role] : [];
  const usingRole = activeRole && roles.includes(activeRole as typeof roles[number]) ? activeRole : user?.role;
  const canReview = usingRole === 'admin' || usingRole === 'knowledge_expert';
  const [track, setTrack] = useState<'manual' | 'knowledge' | 'hermes' | 'jobs' | 'approved'>('manual');
  const [status, setStatus] = useState<CandidateAssetStatus>('pending');
  const [hermesStatus, setHermesStatus] = useState<HermesLearningStatus>('pending_review');
  const [items, setItems] = useState<CandidateAsset[]>([]);
  const [hermesItems, setHermesItems] = useState<HermesLearningAsset[]>([]);
  const [jobs, setJobs] = useState<LearningJob[]>([]);
  const [approvedNotes, setApprovedNotes] = useState('');
  const [loading, setLoading] = useState(false);
  const [editing, setEditing] = useState<CandidateAsset | null>(null);
  const [viewing, setViewing] = useState<CandidateAsset | null>(null);
  const [rejecting, setRejecting] = useState<CandidateAsset | null>(null);
  const [rejectNote, setRejectNote] = useState('');
  const [manualType, setManualType] = useState<CandidateAssetType>('wiki');
  const [manualDescription, setManualDescription] = useState('');
  const [manualImages, setManualImages] = useState<ImageAttachment[]>([]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      if (track === 'manual') {
        return;
      }
      if (track === 'knowledge') {
        setItems(await api.listCandidates(status));
      } else if (track === 'hermes') {
        setHermesItems(await api.listHermesLearningAssets(hermesStatus));
      } else if (track === 'jobs') {
        setJobs(await api.listLearningJobs());
      } else {
        setApprovedNotes(await api.getLearningNotes());
      }
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [track, status, hermesStatus]);

  useEffect(() => {
    if (!canReview && (track === 'hermes' || track === 'jobs')) {
      setTrack('manual');
      return;
    }
    load();
  }, [canReview, load, track]);

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
    setLoading(true);
    try {
      await api.runLearningJob();
      message.success('AI 学习任务已执行');
      setTrack('jobs');
      setJobs(await api.listLearningJobs());
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
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
  }, [fileToImageAttachment, manualImages.length]);

  const createManualDraft = async () => {
    if (!manualDescription.trim() && manualImages.length === 0) {
      message.warning('请先输入知识描述或上传图片');
      return;
    }
    setLoading(true);
    try {
      await api.createManualKnowledgeDraft({
        assetType: manualType,
        description: manualDescription.trim(),
        images: manualImages,
      });
      message.success('已生成候选知识，等待审批');
      setManualDescription('');
      setManualImages([]);
      setStatus('pending');
      setTrack('knowledge');
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  const saveEdit = async () => {
    if (!editing) return;
    try {
      await api.updateCandidate(editing.id, {
        assetType: editing.assetType,
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
        <Title level={4} style={{ margin: 0 }}>知识沉淀 · 审批</Title>
        <Space>
          {canReview && <Button onClick={runLearningJob} loading={loading}>立即 AI 学习</Button>}
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
        </Space>
      </Space>
      <Paragraph type="secondary" style={{ fontSize: 13 }}>
        {canReview ? '双轨学习机制：客服知识走候选审批，Hermes 自学习走审计纠偏。' : '知识专员可录入并维护候选知识，提交后由知识专家或管理员审批。'}
        <Text strong>业务事实必须审批后才可作为正式依据</Text>。
      </Paragraph>

      <Segmented
        style={{ marginBottom: 12 }}
        value={track}
        onChange={(v) => setTrack(v as 'manual' | 'knowledge' | 'hermes' | 'jobs' | 'approved')}
        options={[
          { label: '人工录入', value: 'manual' },
          { label: '客服知识审批', value: 'knowledge' },
          ...(canReview ? [
            { label: 'Hermes 自学习审计', value: 'hermes' },
            { label: 'AI 执行历史', value: 'jobs' },
          ] : []),
          { label: '正式知识', value: 'approved' },
        ]}
      />

      {track === 'knowledge' && (
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
      {track === 'hermes' && (
        <Segmented
          style={{ marginBottom: 16, display: 'block' }}
          value={hermesStatus}
          onChange={(v) => setHermesStatus(v as HermesLearningStatus)}
        options={[
          { label: '待审计', value: 'pending_review' },
          { label: '已保留', value: 'kept' },
          { label: '禁作依据', value: 'prohibited_as_evidence' },
          { label: '已删除', value: 'deleted' },
          { label: '已转候选', value: 'converted' },
        ]}
        />
      )}

      <Card>
        {track === 'manual' && (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Space direction="vertical" size={6} style={{ width: '100%' }}>
              <Text strong>知识类型</Text>
              <Select
                style={{ width: 220 }}
                value={manualType}
                onChange={setManualType}
                options={[
                  { value: 'wiki', label: 'Wiki（完整知识）' },
                  { value: 'faq', label: 'FAQ（标准问答）' },
                ]}
              />
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
                  <Button icon={<PictureOutlined />} disabled={loading || manualImages.length >= MAX_MANUAL_IMAGES}>
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
              <Button type="primary" onClick={createManualDraft} loading={loading}>
                生成候选知识
              </Button>
              <Button onClick={() => {
                setManualDescription('');
                setManualImages([]);
              }}>
                清空
              </Button>
            </Space>
          </Space>
        )}
        {track === 'knowledge' && (items.length === 0 ? (
          <Empty description="暂无候选知识" />
        ) : (
          <Table<CandidateAsset>
            rowKey="id"
            dataSource={items}
            loading={loading}
            pagination={{ pageSize: 10 }}
            columns={[
              {
                title: '类型',
                dataIndex: 'assetType',
                width: 70,
                render: (t: string) => <Tag color={t === 'wiki' ? 'blue' : 'cyan'}>{t.toUpperCase()}</Tag>,
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
        {track === 'hermes' && (hermesItems.length === 0 ? (
          <Empty description="暂无 Hermes 自学习审计记录" />
        ) : (
          <Table<HermesLearningAsset>
            rowKey="id"
            dataSource={hermesItems}
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
                title: '风险标签',
                dataIndex: 'riskFlags',
                width: 230,
                render: (raw?: string) => {
                  const flags = parseEvidence(raw);
                  if (!Array.isArray(flags)) return <Text type="secondary">-</Text>;
                  return flags.map((flag) => <Tag key={String(flag)} color={flag === 'behavior_only' ? 'green' : 'volcano'}>{String(flag)}</Tag>);
                },
              },
              {
                title: '状态',
                dataIndex: 'status',
                width: 110,
                render: (s: HermesLearningStatus) => <Tag color={hermesStatusMeta[s].color}>{hermesStatusMeta[s].label}</Tag>,
              },
              {
                title: '时间',
                dataIndex: 'createdAt',
                width: 130,
                render: (t: string) => dayjs(t).format('MM-DD HH:mm'),
              },
              {
                title: '操作',
                width: 90,
                render: (_, row) => <Button size="small" onClick={() => Modal.info({
                  title: 'Hermes 自学习资产内容',
                  width: 760,
                  content: <pre style={{ whiteSpace: 'pre-wrap', maxHeight: 520, overflow: 'auto', fontSize: 12 }}>{row.content || '（删除记录或内容为空）'}</pre>,
                })}>查看</Button>,
              },
            ]}
          />
        ))}
        {track === 'jobs' && (jobs.length === 0 ? (
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
              这里展示已人工审批发布的正式客服知识。候选知识和 Hermes 自学习审计记录在通过或转化前不会自动进入这里。
            </Paragraph>
            <pre style={{ whiteSpace: 'pre-wrap', maxHeight: 560, overflow: 'auto', fontSize: 13, margin: 0 }}>
              {approvedNotes || '（暂无正式知识；候选知识通过人工审批后会发布到这里）'}
            </pre>
          </Card>
        )}
      </Card>

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
              <Text type="secondary">类型</Text>
              <Select
                style={{ width: '100%' }}
                value={editing.assetType}
                onChange={(v) => setEditing({ ...editing, assetType: v })}
                options={[
                  { value: 'faq', label: 'FAQ（标准问答）' },
                  { value: 'wiki', label: 'Wiki（知识说明）' },
                ]}
              />
            </div>
            <div>
              <Text type="secondary">标题</Text>
              <Input value={editing.title} onChange={(e) => setEditing({ ...editing, title: e.target.value })} />
            </div>
            <div>
              <Text type="secondary">问题（FAQ 标准问法，可空）</Text>
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
