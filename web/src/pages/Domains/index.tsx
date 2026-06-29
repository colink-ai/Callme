import { useEffect, useState } from 'react';
import { Button, Card, Checkbox, Form, Input, List, Select, Space, Typography, message } from 'antd';
import { DeleteOutlined, PlusOutlined, ReloadOutlined, SaveOutlined } from '@ant-design/icons';
import { api, apiErrorMessage } from '../../api/client';
import type { Domain, KnowledgeSource } from '../../types';

const { Title, Text } = Typography;

const emptyDomain = (): Domain => ({
  id: '',
  name: '',
  description: '',
  enabled: true,
  knowledgeSources: [],
});

const emptySource = (domainId: string): KnowledgeSource => ({
  id: '',
  domainId,
  name: '',
  type: 'stdio',
  enabled: true,
  headers: {},
  args: [],
  env: {},
});

function parseLines(value?: string): string[] {
  return (value || '').split('\n').map((item) => item.trim()).filter(Boolean);
}

function parseEnv(value?: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of parseLines(value)) {
    const index = line.indexOf('=');
    if (index <= 0) continue;
    out[line.slice(0, index).trim()] = line.slice(index + 1).trim();
  }
  return out;
}

function formatEnv(value?: Record<string, string>): string {
  return Object.entries(value || {}).map(([k, v]) => `${k}=${v}`).join('\n');
}

export default function DomainsPage() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [selectedID, setSelectedID] = useState('default');
  const [domain, setDomain] = useState<Domain>(emptyDomain());
  const [loading, setLoading] = useState(false);
  const [sourceDraft, setSourceDraft] = useState<KnowledgeSource>(emptySource('default'));
  const [sourceArgsText, setSourceArgsText] = useState('');
  const [sourceEnvText, setSourceEnvText] = useState('');

  const load = async (nextID = selectedID) => {
    setLoading(true);
    try {
      const items = await api.listDomains(true);
      setDomains(items);
      const id = nextID || items[0]?.id || 'default';
      setSelectedID(id);
      const detail = await api.getDomain(id);
      setDomain(detail);
      setSourceDraft(emptySource(id));
      setSourceArgsText('');
      setSourceEnvText('');
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load('default');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const saveDomain = async () => {
    if (!domain.id.trim() || !domain.name.trim()) {
      message.warning('请输入领域 ID 和名称');
      return;
    }
    try {
      const saved = await api.upsertDomain({ ...domain, id: domain.id.trim(), name: domain.name.trim() });
      message.success('领域已保存');
      await load(saved.id);
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const saveSource = async () => {
    if (!sourceDraft.name.trim() || !sourceDraft.type) {
      message.warning('请输入知识源名称和类型');
      return;
    }
    try {
      const source = {
        ...sourceDraft,
        domainId: domain.id,
        args: parseLines(sourceArgsText),
        env: parseEnv(sourceEnvText),
      };
      const saved = await api.upsertKnowledgeSource(domain.id, source);
      message.success('知识源已保存');
      setDomain(saved);
      setSourceDraft(emptySource(domain.id));
      setSourceArgsText('');
      setSourceEnvText('');
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 1120, margin: '0 auto' }}>
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
        <Title level={4} style={{ margin: 0 }}>领域管理</Title>
        <Button icon={<ReloadOutlined />} onClick={() => load()} loading={loading}>刷新</Button>
      </Space>
      <Text type="secondary">
        领域用于隔离会话、Agent Runtime、Skill、Memory 和知识库 MCP 配置。新会话会按所选领域加载对应知识源。
      </Text>

      <div style={{ display: 'grid', gridTemplateColumns: '260px minmax(0, 1fr)', gap: 16, marginTop: 16 }}>
        <Card
          title="领域"
          extra={<Button size="small" icon={<PlusOutlined />} onClick={() => {
            const next = emptyDomain();
            setDomain(next);
            setSelectedID('');
            setSourceDraft(emptySource(''));
          }}>新增</Button>}
        >
          <List
            dataSource={domains}
            renderItem={(item) => (
              <List.Item
                onClick={() => load(item.id)}
                style={{
                  cursor: 'pointer',
                  borderRadius: 6,
                  padding: '10px 12px',
                  background: selectedID === item.id ? '#ecfdf5' : undefined,
                }}
              >
                <Space direction="vertical" size={0}>
                  <Text strong>{item.name}</Text>
                  <Text type="secondary" style={{ fontSize: 12 }}>{item.id}</Text>
                </Space>
              </List.Item>
            )}
          />
        </Card>

        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          <Card title="领域信息">
            <Form layout="vertical">
              <Space size="large" wrap>
                <Form.Item label="领域 ID" required>
                  <Input
                    value={domain.id}
                    disabled={domain.id === 'default'}
                    placeholder="如 ops、dev、hr"
                    onChange={(e) => setDomain((prev) => ({ ...prev, id: e.target.value }))}
                  />
                </Form.Item>
                <Form.Item label="名称" required>
                  <Input value={domain.name} onChange={(e) => setDomain((prev) => ({ ...prev, name: e.target.value }))} />
                </Form.Item>
                <Form.Item label="启用">
                  <Checkbox checked={domain.enabled} onChange={(e) => setDomain((prev) => ({ ...prev, enabled: e.target.checked }))}>
                    允许用户选择
                  </Checkbox>
                </Form.Item>
              </Space>
              <Form.Item label="说明">
                <Input.TextArea rows={3} value={domain.description} onChange={(e) => setDomain((prev) => ({ ...prev, description: e.target.value }))} />
              </Form.Item>
              <Button type="primary" icon={<SaveOutlined />} onClick={saveDomain}>保存领域</Button>
            </Form>
          </Card>

          {domain.id && (
            <Card title="知识源 MCP">
              <List
                dataSource={domain.knowledgeSources ?? []}
                locale={{ emptyText: '暂无知识源' }}
                renderItem={(src) => (
                  <List.Item
                    actions={[
                      <Button key="edit" size="small" onClick={() => {
                        setSourceDraft(src);
                        setSourceArgsText((src.args || []).join('\n'));
                        setSourceEnvText(formatEnv(src.env));
                      }}>编辑</Button>,
                      <Button key="delete" size="small" danger icon={<DeleteOutlined />} onClick={async () => {
                        const saved = await api.deleteKnowledgeSource(domain.id, src.id);
                        setDomain(saved);
                      }} />,
                    ]}
                  >
                    <List.Item.Meta
                      title={<Space><Text strong>{src.name}</Text><Text code>{src.type}</Text>{!src.enabled && <Text type="secondary">已停用</Text>}</Space>}
                      description={src.type === 'http' ? src.url : [src.command, ...(src.args || [])].filter(Boolean).join(' ')}
                    />
                  </List.Item>
                )}
              />

              <Card size="small" title={sourceDraft.id ? '编辑知识源' : '新增知识源'} style={{ marginTop: 16 }}>
                <Space size="large" wrap>
                  <Input style={{ width: 180 }} placeholder="名称" value={sourceDraft.name} onChange={(e) => setSourceDraft((prev) => ({ ...prev, name: e.target.value }))} />
                  <Select
                    style={{ width: 120 }}
                    value={sourceDraft.type}
                    options={[{ value: 'stdio', label: 'stdio' }, { value: 'http', label: 'http' }]}
                    onChange={(value) => setSourceDraft((prev) => ({ ...prev, type: value }))}
                  />
                  <Checkbox checked={sourceDraft.enabled} onChange={(e) => setSourceDraft((prev) => ({ ...prev, enabled: e.target.checked }))}>启用</Checkbox>
                </Space>
                {sourceDraft.type === 'http' ? (
                  <Input style={{ marginTop: 12 }} placeholder="URL" value={sourceDraft.url} onChange={(e) => setSourceDraft((prev) => ({ ...prev, url: e.target.value }))} />
                ) : (
                  <>
                    <Input style={{ marginTop: 12 }} placeholder="启动命令" value={sourceDraft.command} onChange={(e) => setSourceDraft((prev) => ({ ...prev, command: e.target.value }))} />
                    <Input.TextArea style={{ marginTop: 12 }} rows={3} placeholder="参数，每行一个" value={sourceArgsText} onChange={(e) => setSourceArgsText(e.target.value)} />
                    <Input.TextArea style={{ marginTop: 12 }} rows={3} placeholder="环境变量，每行 KEY=VALUE" value={sourceEnvText} onChange={(e) => setSourceEnvText(e.target.value)} />
                  </>
                )}
                <Button type="primary" style={{ marginTop: 12 }} onClick={saveSource}>保存知识源</Button>
              </Card>
            </Card>
          )}
        </Space>
      </div>
    </div>
  );
}
