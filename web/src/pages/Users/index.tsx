import { useEffect, useState } from 'react';
import { Button, InputNumber, Popconfirm, Select, Space, Table, Tag, Typography, message } from 'antd';
import { DeleteOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import { useAuthStore } from '../../store/authStore';
import type { User, UserRole } from '../../types';

const { Title } = Typography;

const roleLabels: Record<UserRole, string> = {
  normal: '普通用户',
  vip: 'VIP 用户',
  knowledge_expert: '知识专家',
  admin: '管理员',
};

const roleColors: Record<UserRole, string> = {
  normal: 'default',
  vip: 'gold',
  knowledge_expert: 'cyan',
  admin: 'red',
};

export default function UsersPage() {
  const [users, setUsers] = useState<User[]>([]);
  const currentUser = useAuthStore((s) => s.user);

  const load = async () => {
    try {
      setUsers(await api.listUsers());
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const updateRole = async (id: string, roles: UserRole[], maxSessions?: number) => {
    try {
      await api.updateUserRole(id, roles, maxSessions);
      message.success('角色已更新');
      await load();
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  const deleteUser = async (id: string) => {
    try {
      await api.deleteUser(id);
      message.success('用户已删除');
      await load();
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto', padding: 24 }}>
      <Title level={4}>用户管理</Title>
      <Table
        rowKey="id"
        dataSource={users}
        columns={[
          { title: '用户名', dataIndex: 'username' },
          {
            title: '角色',
            dataIndex: 'role',
            render: (_: UserRole, record) => {
              const roles = record.roles?.length ? record.roles : [record.role];
              return roles.map((role) => <Tag key={role} color={roleColors[role]}>{roleLabels[role]}</Tag>);
            },
          },
          { title: '注册时间', dataIndex: 'createdAt', render: (v) => dayjs(v).format('YYYY-MM-DD HH:mm') },
          {
            title: '并发会话',
            dataIndex: 'maxSessions',
            width: 120,
            render: (v: number) => `${v || 1} 个`,
          },
          {
            title: '操作',
            render: (_, record) => (
              <Space>
                <Select
                  mode="multiple"
                  value={record.roles?.length ? record.roles : [record.role]}
                  style={{ width: 240 }}
                  onChange={(roles) => updateRole(record.id, roles)}
                  options={[
                    { label: '普通用户', value: 'normal' },
                    { label: 'VIP 用户', value: 'vip' },
                    { label: '知识专家', value: 'knowledge_expert' },
                    { label: '管理员', value: 'admin' },
                  ]}
                />
                <InputNumber
                  min={1}
                  max={50}
                  value={record.maxSessions || 1}
                  addonAfter="并发"
                  style={{ width: 130 }}
                  onChange={(value) => updateRole(record.id, record.roles?.length ? record.roles : [record.role], Number(value) || 1)}
                />
                <Popconfirm
                  title="确认删除该用户？"
                  description="删除后该用户将无法登录，历史会话会保留。"
                  okText="删除"
                  cancelText="取消"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => deleteUser(record.id)}
                  disabled={record.id === currentUser?.id}
                >
                  <Button
                    danger
                    size="small"
                    icon={<DeleteOutlined />}
                    disabled={record.id === currentUser?.id}
                  >
                    删除
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
    </div>
  );
}
