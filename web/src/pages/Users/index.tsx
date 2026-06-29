import { useEffect, useState } from 'react';
import { Button, InputNumber, Popconfirm, Select, Space, Table, Tag, Typography, message } from 'antd';
import { DeleteOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { api, apiErrorMessage } from '../../api/client';
import { useAuthStore } from '../../store/authStore';
import type { Domain, User, UserRole } from '../../types';

const { Title } = Typography;
const DEFAULT_DOMAIN_ID = 'domain-default';

const roleLabels: Record<UserRole, string> = {
  normal: '普通用户',
  vip: 'VIP 用户',
  knowledge_staff: '知识专员',
  knowledge_expert: '知识专家',
  admin: '管理员',
};

const roleColors: Record<UserRole, string> = {
  normal: 'default',
  vip: 'gold',
  knowledge_staff: 'blue',
  knowledge_expert: 'cyan',
  admin: 'red',
};

export default function UsersPage() {
  const [users, setUsers] = useState<User[]>([]);
  const [domains, setDomains] = useState<Domain[]>([]);
  const currentUser = useAuthStore((s) => s.user);

  const load = async () => {
    try {
      const [nextUsers, nextDomains] = await Promise.all([
        api.listUsers(),
        api.listDomains(true),
      ]);
      setUsers(nextUsers);
      setDomains(nextDomains);
    } catch (err) {
      message.error(apiErrorMessage(err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const updateUser = async (user: User, patch: Partial<Pick<User, 'roles' | 'maxSessions' | 'domainIds'>>) => {
    const roles = patch.roles ?? (user.roles?.length ? user.roles : [user.role]);
    const maxSessions = patch.maxSessions ?? user.maxSessions;
    const domainIds = patch.domainIds ?? user.domainIds ?? [DEFAULT_DOMAIN_ID];
    try {
      await api.updateUserRole(user.id, roles, maxSessions, domainIds);
      message.success('用户配置已更新');
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
            title: '可用领域',
            dataIndex: 'domainIds',
            width: 260,
            render: (_: string[] | undefined, record) => (
              <Select
                mode="multiple"
                value={record.domainIds?.length ? record.domainIds : [DEFAULT_DOMAIN_ID]}
                style={{ width: 240 }}
                disabled={record.roles?.includes('admin') || record.role === 'admin'}
                placeholder="选择领域"
                options={domains.map((domain) => ({ label: domain.name, value: domain.id }))}
                onChange={(domainIds) => updateUser(record, { domainIds })}
              />
            ),
          },
          {
            title: '操作',
            render: (_, record) => (
              <Space>
                <Select
                  mode="multiple"
                  value={record.roles?.length ? record.roles : [record.role]}
                  style={{ width: 240 }}
                  onChange={(roles) => updateUser(record, { roles })}
                  options={[
                    { label: '普通用户', value: 'normal' },
                    { label: 'VIP 用户', value: 'vip' },
                    { label: '知识专员', value: 'knowledge_staff' },
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
                  onChange={(value) => updateUser(record, { maxSessions: Number(value) || 1 })}
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
