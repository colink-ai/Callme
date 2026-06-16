// 工单列表：转人工记录与会话上下文包
import { useEffect, useState } from 'react';
import { Button, Card, Space, Table, Tag, Typography, message } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import type { Ticket } from '../../types';

const { Title, Text, Paragraph } = Typography;

const statusMap: Record<Ticket['status'], [string, string]> = {
  open: ['待外发', 'default'],
  notified: ['已外发', 'success'],
  failed: ['外发失败', 'error'],
};

export default function TicketsPage() {
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      setTickets(await api.listTickets());
    } catch (err) {
      message.error(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <Space style={{ marginBottom: 16, width: '100%', justifyContent: 'space-between' }}>
        <Title level={4} style={{ margin: 0 }}>转人工工单</Title>
        <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
      </Space>

      <Card>
        <Table<Ticket>
          rowKey="id"
          dataSource={tickets}
          loading={loading}
          pagination={{ pageSize: 10 }}
          expandable={{
            expandedRowRender: (t) => (
              <Paragraph style={{ whiteSpace: 'pre-wrap', maxHeight: 360, overflow: 'auto', margin: 0, fontSize: 13 }}>
                {t.transcript || '（无会话内容）'}
              </Paragraph>
            ),
          }}
          columns={[
            { title: '工单号', dataIndex: 'id', render: (id: string) => <Text code>{id.slice(0, 8)}</Text> },
            { title: '来源会话', dataIndex: 'sessionId', render: (id: string) => <Text type="secondary">{id.slice(0, 8)}</Text> },
            { title: '原因', dataIndex: 'reason', ellipsis: true },
            {
              title: '状态',
              dataIndex: 'status',
              render: (s: Ticket['status']) => {
                const [label, color] = statusMap[s] ?? [s, 'default'];
                return <Tag color={color}>{label}</Tag>;
              },
            },
            {
              title: '创建时间',
              dataIndex: 'createdAt',
              render: (t: string) => dayjs(t).format('MM-DD HH:mm:ss'),
            },
          ]}
        />
      </Card>
    </div>
  );
}
