import { useEffect, useState } from 'react';
import { Button, List, Modal, Space, Table, Tag, Typography, message } from 'antd';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import type { Message, Session } from '../../types';

const { Text, Title } = Typography;
const ASSISTANT_DISPLAY_NAME = 'Callme 助手';

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

export default function HistoryPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [open, setOpen] = useState(false);

  const load = async () => {
    try {
      setSessions(await api.listMySessions());
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const view = async (session: Session) => {
    try {
      setMessages(await api.listMessages(session.id));
      setOpen(true);
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto', padding: 24 }}>
      <Title level={4}>历史会话</Title>
      <Table
        rowKey="id"
        dataSource={sessions}
        pagination={{ pageSize: 10 }}
        columns={[
          { title: '首问', dataIndex: 'title', render: (v) => v || '未提问' },
          {
            title: '状态',
            dataIndex: 'status',
            render: (v) => <Tag color={v === 'closed' ? 'default' : 'green'}>{v}</Tag>,
          },
          { title: '开始时间', dataIndex: 'createdAt', render: (v) => dayjs(v).format('YYYY-MM-DD HH:mm') },
          {
            title: '操作',
            render: (_, record) => <Button size="small" onClick={() => view(record)}>查看提问</Button>,
          },
        ]}
      />
      <Modal title="会话记录" open={open} onCancel={() => setOpen(false)} footer={null} width={760}>
        <List
          dataSource={messages}
          renderItem={(m) => {
            const runtimeLabel = [formatAgentType(m.agentType), m.model].filter(Boolean).join(' · ');
            return (
              <List.Item>
                <Space direction="vertical" size={2} style={{ width: '100%' }}>
                  <Space>
                    <Tag color={m.role === 'user' ? 'blue' : 'green'}>{m.role === 'user' ? '用户' : ASSISTANT_DISPLAY_NAME}</Tag>
                    {m.role === 'assistant' && runtimeLabel && <Tag color="green">{runtimeLabel}</Tag>}
                    <Text type="secondary">{dayjs(m.createdAt).format('YYYY-MM-DD HH:mm:ss')}</Text>
                  </Space>
                  <Text>{m.content}</Text>
                </Space>
              </List.Item>
            );
          }}
        />
      </Modal>
    </div>
  );
}
