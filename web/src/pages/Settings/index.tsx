// 设置页：多套 Agent / 模型配置档案、坐席容量
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  List,
  Popconfirm,
  Select,
  Space,
  Switch,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  ApiOutlined,
  CheckCircleOutlined,
  DeleteOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import { api, apiErrorMessage } from '../../api/client';
import type { AgentProfile, AgentProfilesSettings, AgentSettings, PoolSettings } from '../../types';

const { Text, Title } = Typography;

type AgentProfileForm = AgentSettings & {
  profileName: string;
};

const emptyAgentSettings = (type = 'hermes', cliPath = ''): AgentSettings => ({
  type,
  cliPath,
  defaultModel: '',
  apiUrl: '',
  apiToken: '',
  systemPrompt: '',
  supportsMultimodal: false,
});

const makeProfile = (settings: AgentSettings, index: number): AgentProfile => ({
  id: `profile-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`,
  name: `配置 ${index}`,
  settings: { ...settings, apiToken: '' },
});

export default function SettingsPage() {
  const [agentForm] = Form.useForm<AgentProfileForm>();
  const [poolForm] = Form.useForm<PoolSettings>();
  const [types, setTypes] = useState<{ type: string; name: string; defaultPath?: string }[]>([]);
  const [agentProfiles, setAgentProfiles] = useState<AgentProfilesSettings | null>(null);
  const [selectedProfileId, setSelectedProfileId] = useState('');
  const [agentChecking, setAgentChecking] = useState(false);
  const [agentSaving, setAgentSaving] = useState(false);

  const selectedProfile = useMemo(
    () => agentProfiles?.profiles.find((p) => p.id === selectedProfileId),
    [agentProfiles, selectedProfileId],
  );

  const defaultTypePath = useCallback(
    (type: string) => types.find((t) => t.type === type)?.defaultPath ?? '',
    [types],
  );

  const applyProfileToForm = useCallback(
    (profile?: AgentProfile) => {
      if (!profile) return;
      agentForm.setFieldsValue({
        ...profile.settings,
        profileName: profile.name,
      });
    },
    [agentForm],
  );

  const mergeCurrentForm = useCallback((): AgentProfilesSettings | null => {
    if (!agentProfiles || !selectedProfileId) return agentProfiles;
    const values = agentForm.getFieldsValue();
    return {
      ...agentProfiles,
      profiles: agentProfiles.profiles.map((p) => {
        if (p.id !== selectedProfileId) return p;
        const { profileName, ...settings } = values;
        return {
          ...p,
          name: (profileName || p.name).trim(),
          settings: {
            ...p.settings,
            ...settings,
          },
        };
      }),
    };
  }, [agentForm, agentProfiles, selectedProfileId]);

  useEffect(() => {
    (async () => {
      try {
        const [a, p, t] = await Promise.all([
          api.getAgentSettings(),
          api.getPoolSettings(),
          api.getAgentTypes(),
        ]);
        setTypes(t);
        setAgentProfiles(a);
        const active = a.activeProfileId || a.profiles[0]?.id || '';
        setSelectedProfileId(active);
        applyProfileToForm(a.profiles.find((profile) => profile.id === active) ?? a.profiles[0]);
        poolForm.setFieldsValue(p);
      } catch (err) {
        message.error(apiErrorMessage(err));
      }
    })();
  }, [applyProfileToForm, poolForm]);

  const selectProfile = (profileId: string) => {
    const merged = mergeCurrentForm();
    if (!merged) return;
    const nextProfile = merged.profiles.find((p) => p.id === profileId);
    setAgentProfiles(merged);
    setSelectedProfileId(profileId);
    applyProfileToForm(nextProfile);
  };

  const saveProfiles = async (next: AgentProfilesSettings, successText: string) => {
    setAgentSaving(true);
    try {
      const saved = await api.updateAgentSettings(next);
      setAgentProfiles(saved);
      const selected = saved.profiles.find((p) => p.id === selectedProfileId)
        ?? saved.profiles.find((p) => p.id === saved.activeProfileId)
        ?? saved.profiles[0];
      if (selected) {
        setSelectedProfileId(selected.id);
        applyProfileToForm(selected);
      }
      message.success(successText);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setAgentSaving(false);
    }
  };

  const saveAgent = async () => {
    try {
      await agentForm.validateFields();
      const merged = mergeCurrentForm();
      if (!merged) return;
      await saveProfiles(merged, '已保存，新会话将使用当前启用的模型配置');
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const switchActiveProfile = async (profileId = selectedProfileId) => {
    const merged = mergeCurrentForm();
    if (!merged || !profileId) return;
    await saveProfiles({ ...merged, activeProfileId: profileId }, '已切换当前启用配置，新会话生效');
  };

  const addProfile = () => {
    const merged = mergeCurrentForm();
    if (!merged) return;
    const current = selectedProfile?.settings
      ?? emptyAgentSettings('hermes', defaultTypePath('hermes'));
    const nextProfile = makeProfile(current, merged.profiles.length + 1);
    const next = {
      ...merged,
      profiles: [...merged.profiles, nextProfile],
    };
    setAgentProfiles(next);
    setSelectedProfileId(nextProfile.id);
    applyProfileToForm(nextProfile);
  };

  const deleteProfile = () => {
    if (!agentProfiles || !selectedProfileId || agentProfiles.profiles.length <= 1) return;
    const nextProfiles = agentProfiles.profiles.filter((p) => p.id !== selectedProfileId);
    const nextActive = agentProfiles.activeProfileId === selectedProfileId
      ? nextProfiles[0].id
      : agentProfiles.activeProfileId;
    const next = {
      ...agentProfiles,
      activeProfileId: nextActive,
      profiles: nextProfiles,
    };
    const nextSelected = nextProfiles.find((p) => p.id === nextActive) ?? nextProfiles[0];
    setAgentProfiles(next);
    setSelectedProfileId(nextSelected.id);
    applyProfileToForm(nextSelected);
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
      if (r.healthy) message.success('当前启用的 Agent CLI 连接正常');
      else message.error(`Agent 不可用：${r.error}`);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setAgentChecking(false);
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 1040, margin: '0 auto' }}>
      <Title level={4} style={{ marginTop: 0 }}>设置</Title>

      <Card
        title="Agent 与模型"
        style={{ marginBottom: 16 }}
        extra={
          <Space wrap>
            <Select
              value={agentProfiles?.activeProfileId}
              style={{ minWidth: 180 }}
              options={(agentProfiles?.profiles ?? []).map((p) => ({ value: p.id, label: p.name }))}
              onChange={switchActiveProfile}
            />
            <Button icon={<ApiOutlined />} onClick={checkAgent} loading={agentChecking}>
              连通性检查
            </Button>
          </Space>
        }
      >
        <div style={{ display: 'grid', gridTemplateColumns: '240px minmax(0, 1fr)', gap: 24 }}>
          <div>
            <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
              <Text strong>配置档案</Text>
              <Button size="small" icon={<PlusOutlined />} onClick={addProfile}>
                新增
              </Button>
            </Space>
            <List
              size="small"
              dataSource={agentProfiles?.profiles ?? []}
              renderItem={(profile) => {
                const active = profile.id === agentProfiles?.activeProfileId;
                const selected = profile.id === selectedProfileId;
                return (
                  <List.Item
                    onClick={() => selectProfile(profile.id)}
                    style={{
                      cursor: 'pointer',
                      borderRadius: 6,
                      padding: '10px 12px',
                      marginBottom: 8,
                      border: selected ? '1px solid #10b981' : '1px solid #f0f0f0',
                      background: selected ? '#ecfdf5' : '#fff',
                    }}
                  >
                    <Space direction="vertical" size={2} style={{ width: '100%' }}>
                      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                        <Text strong ellipsis style={{ maxWidth: 132 }}>{profile.name}</Text>
                        {active && <Tag color="green">当前</Tag>}
                      </Space>
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {profile.settings.type || 'hermes'} · {profile.settings.defaultModel || '未配置模型'}
                      </Text>
                    </Space>
                  </List.Item>
                );
              }}
            />
          </div>

          <Form form={agentForm} layout="vertical">
            <Form.Item
              label="配置名称"
              name="profileName"
              rules={[{ required: true, message: '请输入配置名称' }]}
            >
              <Input placeholder="如：生产 GLM、测试 OpenCode、备用 Claude" />
            </Form.Item>
            <Space size="large" wrap>
              <Form.Item label="Agent 类型" name="type" style={{ minWidth: 200 }}>
                <Select
                  options={types.map((t) => ({ value: t.type, label: t.name }))}
                  onChange={(value) => {
                    const selected = types.find((t) => t.type === value);
                    agentForm.setFieldValue('cliPath', selected?.defaultPath ?? '');
                  }}
                />
              </Form.Item>
              <Form.Item
                label="CLI 路径"
                name="cliPath"
                style={{ minWidth: 240 }}
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
              <Form.Item
                label="支持多模态"
                name="supportsMultimodal"
                valuePropName="checked"
                tooltip="开启后允许用户和内部 AI 任务提交图片；默认关闭，避免把图片发给不支持视觉能力的模型。"
              >
                <Switch checkedChildren="支持" unCheckedChildren="关闭" />
              </Form.Item>
            </Space>
            <Form.Item
              label="客服系统提示词"
              name="systemPrompt"
              tooltip="建议加入自学习约束：单次会话结论不得泛化为全局规则；业务事实须走候选资产、经人工审批后才生效；可参考 Runtime 工作目录中的 approved_knowledge.md。"
            >
              <Input.TextArea rows={6} placeholder="定义客服角色、知识检索策略、转人工规则；并约束：业务事实须经审批沉淀、单次会话结论不得泛化…" />
            </Form.Item>
            <Space wrap>
              <Button type="primary" onClick={saveAgent} loading={agentSaving}>
                保存配置
              </Button>
              <Button icon={<CheckCircleOutlined />} onClick={() => switchActiveProfile()} loading={agentSaving}>
                设为当前启用
              </Button>
              <Popconfirm
                title="删除这个配置档案？"
                description="删除后需要保存才会写入后端。"
                onConfirm={deleteProfile}
                disabled={(agentProfiles?.profiles.length ?? 0) <= 1}
              >
                <Button
                  danger
                  icon={<DeleteOutlined />}
                  disabled={(agentProfiles?.profiles.length ?? 0) <= 1}
                >
                  删除配置
                </Button>
              </Popconfirm>
            </Space>
          </Form>
        </div>
      </Card>

      <Card title="坐席与排队（并发控制）" style={{ marginBottom: 16 }}>
        <Form form={poolForm} layout="inline">
          <Form.Item
            label="坐席数（最大并发会话）"
            name="maxActive"
            rules={[{ required: true }]}
            tooltip="同时服务的会话上限，等于并发 Agent 会话数；超出进入排队"
          >
            <InputNumber min={1} max={100} />
          </Form.Item>
          <Form.Item label="最大排队人数" name="maxQueue" rules={[{ required: true }]}>
            <InputNumber min={0} max={1000} />
          </Form.Item>
          <Button type="primary" onClick={savePool}>保存</Button>
        </Form>
      </Card>
    </div>
  );
}
