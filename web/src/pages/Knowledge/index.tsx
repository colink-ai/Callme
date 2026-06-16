// 知识检索页：直接检索代码图谱 / wiki 知识图谱
// 与"智能客服"是包含关系——客服回答时 Agent 自动调用这些知识源（MCP 工具），
// 本页面提供独立的直查入口，便于运营核对知识源质量与排查命中问题。
import { useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Empty,
  Input,
  Select,
  Space,
  Spin,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  FileSearchOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api, apiErrorMessage } from '../../api/client';
import type { KnowledgeSourceInfo } from '../../types';

const { Title, Text } = Typography;

interface QueryResultItem {
  source: string;
  displayName: string;
  content?: string;
  error?: string;
}

export default function KnowledgePage() {
  const [sources, setSources] = useState<KnowledgeSourceInfo[]>([]);
  const [selected, setSelected] = useState<string>('__all__');
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<QueryResultItem[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    api.listKnowledgeSources().then(setSources).catch((err) => message.error(apiErrorMessage(err)));
  }, []);

  const checkHealth = async () => {
    setChecking(true);
    try {
      setSources(await api.checkKnowledgeHealth());
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setChecking(false);
    }
  };

  const displayNameOf = (name: string) =>
    sources.find((s) => s.name === name)?.displayName || name;

  const onSearch = async () => {
    const q = query.trim();
    if (!q) return;
    const targets =
      selected === '__all__'
        ? sources.filter((s) => s.transport === 'http').map((s) => s.name)
        : [selected];
    if (targets.length === 0) {
      message.warning('没有可直查的 http 类型知识源');
      return;
    }
    setSearching(true);
    setResults(null);
    const items = await Promise.all(
      targets.map(async (name): Promise<QueryResultItem> => {
        try {
          const r = await api.queryKnowledge(name, q);
          return { source: name, displayName: displayNameOf(name), content: r.content };
        } catch (err) {
          return { source: name, displayName: displayNameOf(name), error: apiErrorMessage(err) };
        }
      }),
    );
    setResults(items);
    setSearching(false);
  };

  return (
    <div style={{ padding: 24, maxWidth: 920, margin: '0 auto' }}>
      <Title level={4} style={{ marginTop: 0 }}>
        <FileSearchOutlined /> 知识检索
      </Title>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="智能客服回答问题时会自动调用以下知识源（代码图谱 / Wiki 知识图谱）。本页面提供直接检索入口，便于核对知识源内容与排查回答依据。"
      />

      <Card
        title="知识源状态"
        size="small"
        style={{ marginBottom: 16 }}
        extra={<Button size="small" loading={checking} onClick={checkHealth}>健康检查</Button>}
      >
        <Space wrap>
          {sources.map((s) => (
            <Tag
              key={s.name}
              icon={
                s.healthy === undefined ? undefined : s.healthy ? (
                  <CheckCircleOutlined />
                ) : (
                  <CloseCircleOutlined />
                )
              }
              color={s.healthy === undefined ? 'default' : s.healthy ? 'success' : 'error'}
              style={{ padding: '4px 10px' }}
            >
              {s.displayName || s.name}（{s.transport}）
            </Tag>
          ))}
          {sources.length === 0 && (
            <Text type="secondary">尚未配置知识源，请在 Agent 本地 MCP 配置中添加</Text>
          )}
        </Space>
      </Card>

      <Space.Compact style={{ width: '100%', marginBottom: 16 }}>
        <Select
          value={selected}
          onChange={setSelected}
          style={{ width: 200 }}
          options={[
            { value: '__all__', label: '全部知识源' },
            ...sources.map((s) => ({ value: s.name, label: s.displayName || s.name })),
          ]}
        />
        <Input
          placeholder="输入检索关键词，如：会话超时配置"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onPressEnter={onSearch}
          allowClear
        />
        <Button type="primary" icon={<SearchOutlined />} loading={searching} onClick={onSearch}>
          检索
        </Button>
      </Space.Compact>

      {searching && (
        <div style={{ textAlign: 'center', padding: 40 }}>
          <Spin tip="检索中…" />
        </div>
      )}

      {results?.map((r) => (
        <Card
          key={r.source}
          size="small"
          title={
            <Space>
              <FileSearchOutlined style={{ color: 'var(--color-primary)' }} />
              {r.displayName}
            </Space>
          }
          style={{ marginBottom: 12 }}
        >
          {r.error ? (
            <Text type="danger">{r.error}</Text>
          ) : r.content ? (
            <div style={{ fontSize: 13 }}>
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{r.content}</ReactMarkdown>
            </div>
          ) : (
            <Text type="secondary">无匹配结果</Text>
          )}
        </Card>
      ))}

      {results !== null && results.length === 0 && <Empty description="无结果" />}
    </div>
  );
}
