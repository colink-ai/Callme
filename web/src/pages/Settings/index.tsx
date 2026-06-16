// 设置页：模型切换（Hermes 配置）、坐席容量、知识源健康、学习笔记查看
import { useEffect, useState } from 'react';
import {
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Tag,
  Typography,
  message,
} from 'antd';
import { ApiOutlined, BookOutlined, CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { api, apiErrorMessage } from '../../api/client';
import type { AgentSettings, KnowledgeSourceInfo, PoolSettings } from '../../types';

const { Title, Text } = Typography;

export default function SettingsPage() {
  const [agentForm] = Form.useForm<AgentSettings>();
  const [poolForm] = Form.useForm<PoolSettings>();
  const [types, setTypes] = useState<{ type: string; name: string; defaultPath?: string }[]>([]);
  const [sources, setSources] = useState<KnowledgeSourceInfo[]>([]);
  const [healthChecking, setHealthChecking] = useState(false);
  const [agentChecking, setAgentChecking] = useState(false);
  const [notesOpen, setNotesOpen] = useState(false);
  const [notes, setNotes] = useState('');

  useEffect(() => {
    (async () => {
      try {
        const [a, p, t, s] = await Promise.all([
          api.getAgentSettings(),
          api.getPoolSettings(),
          api.getAgentTypes(),
          api.listKnowledgeSources(),
        ]);
        agentForm.setFieldsValue(a);
        poolForm.setFieldsValue(p);
        setTypes(t);
        setSources(s);
      } catch (err) {
        message.error(apiErrorMessage(err));
      }
    })();
  }, [agentForm, poolForm]);

  const saveAgent = async () => {
    try {
      const values = await agentForm.validateFields();
      const saved = await api.updateAgentSettings(values);
      agentForm.setFieldsValue(saved);
      message.success('已保存，新会话将使用新的模型配置');
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const savePool = async () => {
    try {
      const values = await poolForm.validateFields();
      await api.updatePoolSettings(values);
      message.success('坐席配置已保存');
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const checkAgent = async () => {
    setAgentChecking(true);
    try {
      const r = await api.checkAgentHealth();
      if (r.healthy) message.success('Agent CLI 连接正常');
      else message.error(`Agent 不可用：${r.error}`);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setAgentChecking(false);
    }
  };

  const checkKnowledge = async () => {
    setHealthChecking(true);
    try {
      setSources(await api.checkKnowledgeHealth());
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setHealthChecking(false);
    }
  };

  const showNotes = async () => {
    try {
      setNotes(await api.getLearningNotes());
      setNotesOpen(true);
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 860, margin: '0 auto' }}>
      <Title level={4} style={{ marginTop: 0 }}>设置</Title>

      <Card
        title="Agent 与模型"
        style={{ marginBottom: 16 }}
        extra={
          <Button icon={<ApiOutlined />} onClick={checkAgent} loading={agentChecking}>
            连通性检查
          </Button>
        }
      >
        <Form form={agentForm} layout="vertical">
          <Space size="large" wrap>
            <Form.Item label="Agent 类型" name="type" style={{ minWidth: 200 }}>
              <Select
                options={types.map((t) => ({ value: t.type, label: t.name }))}
                onChange={(value) => {
                  const selected = types.find((t) => t.type === value);
                  if (selected?.defaultPath) {
                    agentForm.setFieldValue('cliPath', selected.defaultPath);
                  }
                }}
              />
            </Form.Item>
            <Form.Item
              label="CLI 路径"
              name="cliPath"
              style={{ minWidth: 200 }}
              tooltip="可选；留空时后端使用当前 Agent 类型的默认 CLI 路径"
            >
              <Input placeholder="留空使用默认路径" />
            </Form.Item>
            <Form.Item
              label="模型"
              name="defaultModel"
              rules={[{ required: true, message: '请输入模型 ID' }]}
              style={{ minWidth: 260 }}
              tooltip="保存后对新会话生效"
            >
              <Input placeholder="如 claude-sonnet-4-6" />
            </Form.Item>
          </Space>
          <Space size="large" wrap style={{ width: '100%' }}>
            <Form.Item label="API Base URL（自定义 provider，可选）" name="apiUrl" style={{ minWidth: 340 }}>
              <Input placeholder="https://your-llm-gateway/v1" />
            </Form.Item>
            <Form.Item label="API Token（可选）" name="apiToken" style={{ minWidth: 280 }}>
              <Input.Password placeholder="留空或保持掩码则不修改" />
            </Form.Item>
          </Space>
          <Form.Item label="客服系统提示词" name="systemPrompt">
            <Input.TextArea rows={5} placeholder="定义客服的角色、知识检索策略与转人工规则…" />
          </Form.Item>
          <Button type="primary" onClick={saveAgent}>保存模型配置</Button>
        </Form>
      </Card>

      <Card title="坐席与排队（并发控制）" style={{ marginBottom: 16 }}>
        <Form form={poolForm} layout="inline">
          <Form.Item
            label="坐席数（最大并发会话）"
            name="maxActive"
            rules={[{ required: true }]}
            tooltip="同时服务的会话上限，等于并发 Hermes 进程数；超出进入排队"
          >
            <InputNumber min={1} max={100} />
          </Form.Item>
          <Form.Item label="最大排队人数" name="maxQueue" rules={[{ required: true }]}>
            <InputNumber min={0} max={1000} />
          </Form.Item>
          <Button type="primary" onClick={savePool}>保存</Button>
        </Form>
      </Card>

      <Card
        title="知识源（MCP）"
        style={{ marginBottom: 16 }}
        extra={
          <Button onClick={checkKnowledge} loading={healthChecking}>健康检查</Button>
        }
      >
        <Descriptions column={1} size="small">
          {sources.map((s) => (
            <Descriptions.Item key={s.name} label={s.displayName || s.name}>
              <Space>
                <Tag>{s.transport}</Tag>
                {s.healthy === undefined ? (
                  <Text type="secondary">未检查</Text>
                ) : s.healthy ? (
                  <Tag icon={<CheckCircleOutlined />} color="success">正常</Tag>
                ) : (
                  <Tag icon={<CloseCircleOutlined />} color="error">不可用</Tag>
                )}
              </Space>
            </Descriptions.Item>
          ))}
          {sources.length === 0 && (
            <Descriptions.Item label="提示">
              <Text type="secondary">尚未配置知识源，请在 Agent 本地 MCP 配置中添加</Text>
            </Descriptions.Item>
          )}
        </Descriptions>
      </Card>

      <Card title="自学习">
        <Space direction="vertical">
          <Text type="secondary">
            用户反馈（点踩 + 纠错）会被定时蒸馏为学习笔记写入共享 HERMES_HOME，与 Hermes 自身记忆共同累积，使客服越用越聪明。
          </Text>
          <Button icon={<BookOutlined />} onClick={showNotes}>查看学习笔记</Button>
        </Space>
      </Card>

      <Modal
        title="学习笔记（learning_notes.md）"
        open={notesOpen}
        footer={null}
        width={720}
        onCancel={() => setNotesOpen(false)}
      >
        <pre style={{ whiteSpace: 'pre-wrap', maxHeight: 480, overflow: 'auto', fontSize: 13 }}>
          {notes || '（暂无学习笔记，提交带纠错的点踩反馈后由蒸馏任务生成）'}
        </pre>
      </Modal>
    </div>
  );
}
