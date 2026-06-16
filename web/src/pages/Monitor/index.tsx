// 运营端会话监控页：活跃会话（开始时间/持续时长）、排队队列、强制结束
import { useEffect, useState } from 'react';
import { Button, Card, DatePicker, Popconfirm, Select, Space, Table, Tag, Typography, message } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import type { Session, SessionView, User } from '../../types';

const { RangePicker } = DatePicker;
const { Title, Text } = Typography;

function fmtDuration(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}小时${m}分`;
  if (m > 0) return `${m}分${s}秒`;
  return `${s}秒`;
}

function renderUser(row: Session) {
  if (row.username) return <Text strong>{row.username}</Text>;
  if (row.userId) return <Text type="secondary">{row.userId.slice(0, 8)}</Text>;
  return <Text type="secondary">未知用户</Text>;
}

function renderSessionDuration(row: Session) {
  if (!row.startedAt || !row.closedAt) return <Text type="secondary">-</Text>;
  const seconds = Math.max(0, dayjs(row.closedAt).diff(dayjs(row.startedAt), 'second'));
  return <Tag color="processing">{fmtDuration(seconds)}</Tag>;
}

export default function MonitorPage() {
  const [active, setActive] = useState<SessionView[]>([]);
  const [queued, setQueued] = useState<SessionView[]>([]);
  const [closed, setClosed] = useState<Session[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [closedUserId, setClosedUserId] = useState<string | undefined>();
  const [closedTotal, setClosedTotal] = useState(0);
  const [closedPage, setClosedPage] = useState(1);
  const [closedPageSize, setClosedPageSize] = useState(10);
  const [closedRange, setClosedRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
  const [loading, setLoading] = useState(false);
  const [closedLoading, setClosedLoading] = useState(false);

  const loadLive = async () => {
    setLoading(true);
    try {
      const data = await api.listLiveSessions(false);
      setActive(data.active);
      setQueued(data.queued);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  const loadClosed = async () => {
    setClosedLoading(true);
    try {
      const data = await api.listClosedSessions({
        start: closedRange?.[0]?.toISOString(),
        end: closedRange?.[1]?.toISOString(),
        userId: closedUserId,
        page: closedPage,
        pageSize: closedPageSize,
      });
      setClosed(data.sessions);
      setClosedTotal(data.total);
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setClosedLoading(false);
    }
  };

  useEffect(() => {
    loadLive();
    const t = window.setInterval(loadLive, 5000);
    return () => window.clearInterval(t);
  }, []);

  useEffect(() => {
    api.listUsers().then(setUsers).catch(() => {
      /* 用户筛选加载失败不影响监控主流程 */
    });
  }, []);

  useEffect(() => {
    loadClosed();
  }, [closedPage, closedPageSize, closedRange, closedUserId]);

  const forceClose = async (id: string) => {
    try {
      await api.closeSession(id, true);
      message.success('会话已结束');
      await Promise.all([loadLive(), loadClosed()]);
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <Space style={{ marginBottom: 16, justifyContent: 'space-between', width: '100%' }}>
        <Title level={4} style={{ margin: 0 }}>会话监控</Title>
        <Button icon={<ReloadOutlined />} onClick={() => { loadLive(); loadClosed(); }} loading={loading || closedLoading}>刷新</Button>
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
            { title: '用户', render: (_, row) => renderUser(row) },
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
            { title: '用户', render: (_, row) => renderUser(row) },
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

      <Card
        title="最近结束的会话"
        extra={
          <Space wrap>
            <Select
              allowClear
              showSearch
              placeholder="筛选用户"
              style={{ width: 180 }}
              value={closedUserId}
              optionFilterProp="label"
              options={users.map((u) => ({ value: u.id, label: u.username }))}
              onChange={(userId) => {
                setClosedUserId(userId);
                setClosedPage(1);
              }}
            />
            <RangePicker
              showTime
              allowClear
              format="YYYY-MM-DD HH:mm"
              value={closedRange}
              onChange={(range) => {
                setClosedRange(range ? [range[0], range[1]] : null);
                setClosedPage(1);
              }}
            />
          </Space>
        }
      >
        <Table<Session>
          rowKey="id"
          dataSource={closed}
          loading={closedLoading}
          pagination={{
            current: closedPage,
            pageSize: closedPageSize,
            total: closedTotal,
            showSizeChanger: true,
            showTotal: (total) => `共 ${total} 条`,
            onChange: (page, pageSize) => {
              setClosedPage(page);
              setClosedPageSize(pageSize);
            },
          }}
          size="small"
          columns={[
            { title: '会话', dataIndex: 'id', render: (id: string) => <Text code>{id.slice(0, 8)}</Text> },
            { title: '用户', render: (_, row) => renderUser(row) },
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
            { title: '时长', render: (_, row) => renderSessionDuration(row) },
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
