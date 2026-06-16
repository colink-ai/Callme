// 运营端会话监控页：活跃会话（开始时间/持续时长）、排队队列、强制结束
import { useEffect, useState } from 'react';
import { Button, Card, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import type { Session, SessionView } from '../../types';

const { Title, Text } = Typography;

function fmtDuration(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}小时${m}分`;
  if (m > 0) return `${m}分${s}秒`;
  return `${s}秒`;
}

export default function MonitorPage() {
  const [active, setActive] = useState<SessionView[]>([]);
  const [queued, setQueued] = useState<SessionView[]>([]);
  const [closed, setClosed] = useState<Session[]>([]);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const data = await api.listLiveSessions(true);
      setActive(data.active);
      setQueued(data.queued);
      setClosed(data.closed);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    const t = window.setInterval(load, 5000);
    return () => window.clearInterval(t);
  }, []);

  const forceClose = async (id: string) => {
    try {
      await api.closeSession(id, true);
      message.success('会话已结束');
      load();
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <Space style={{ marginBottom: 16, justifyContent: 'space-between', width: '100%' }}>
        <Title level={4} style={{ margin: 0 }}>会话监控</Title>
        <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
      </Space>

      <Card title={`活跃会话（${active.length} 个坐席占用中）`} style={{ marginBottom: 16 }}>
        <Table<SessionView>
          rowKey="id"
          dataSource={active}
          pagination={false}
          size="small"
          locale={{ emptyText: '暂无活跃会话' }}
          columns={[
            { title: '会话', dataIndex: 'id', render: (id: string) => <Text code>{id.slice(0, 8)}</Text> },
            { title: '首问', dataIndex: 'title', ellipsis: true, render: (t: string) => t || <Text type="secondary">（尚未提问）</Text> },
            {
              title: '开始时间',
              dataIndex: 'startedAt',
              render: (t?: string) => (t ? dayjs(t).format('MM-DD HH:mm:ss') : '-'),
            },
            {
              title: '持续时长',
              dataIndex: 'durationSeconds',
              render: (s: number) => <Tag color="processing">{fmtDuration(s)}</Tag>,
            },
            { title: '客户端', dataIndex: 'clientId', render: (c: string) => <Text type="secondary">{c.slice(0, 8)}</Text> },
            {
              title: '操作',
              render: (_, row) => (
                <Popconfirm title="确认强制结束该会话？" onConfirm={() => forceClose(row.id)}>
                  <Button danger size="small">强制结束</Button>
                </Popconfirm>
              ),
            },
          ]}
        />
      </Card>

      <Card title={`排队队列（${queued.length} 人等待）`} style={{ marginBottom: 16 }}>
        <Table<SessionView>
          rowKey="id"
          dataSource={queued}
          pagination={false}
          size="small"
          locale={{ emptyText: '当前无排队' }}
          columns={[
            { title: '位置', dataIndex: 'position', width: 70, render: (p: number) => <Tag>{p}</Tag> },
            { title: '会话', dataIndex: 'id', render: (id: string) => <Text code>{id.slice(0, 8)}</Text> },
            {
              title: '进入时间',
              dataIndex: 'createdAt',
              render: (t: string) => dayjs(t).format('HH:mm:ss'),
            },
            {
              title: '已等待',
              dataIndex: 'waitingSeconds',
              render: (s: number) => <Tag color="warning">{fmtDuration(s)}</Tag>,
            },
            {
              title: '操作',
              render: (_, row) => (
                <Popconfirm title="确认移出队列？" onConfirm={() => forceClose(row.id)}>
                  <Button danger size="small">移出</Button>
                </Popconfirm>
              ),
            },
          ]}
        />
      </Card>

      <Card title="最近结束的会话">
        <Table<Session>
          rowKey="id"
          dataSource={closed}
          pagination={{ pageSize: 10 }}
          size="small"
          columns={[
            { title: '会话', dataIndex: 'id', render: (id: string) => <Text code>{id.slice(0, 8)}</Text> },
            { title: '首问', dataIndex: 'title', ellipsis: true },
            {
              title: '开始',
              dataIndex: 'startedAt',
              render: (t?: string) => (t ? dayjs(t).format('MM-DD HH:mm') : '-'),
            },
            {
              title: '结束',
              dataIndex: 'closedAt',
              render: (t?: string) => (t ? dayjs(t).format('MM-DD HH:mm') : '-'),
            },
            {
              title: '结束原因',
              dataIndex: 'closeReason',
              render: (r: string) => {
                const map: Record<string, [string, string]> = {
                  user: ['用户结束', 'default'],
                  idle: ['空闲超时', 'warning'],
                  max_time: ['超最大时长', 'warning'],
                  admin: ['运营结束', 'error'],
                  error: ['异常', 'error'],
                  queue_leave: ['离开排队', 'default'],
                };
                const [label, color] = map[r] ?? [r, 'default'];
                return <Tag color={color}>{label}</Tag>;
              },
            },
          ]}
        />
      </Card>
    </div>
  );
}
