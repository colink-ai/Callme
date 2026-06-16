import { useState } from 'react';
import { Alert, Button, Card, Form, Input, Segmented, Typography } from 'antd';
import { CustomerServiceOutlined } from '@ant-design/icons';
import { apiErrorMessage } from '../../api/client';
import { useAuthStore } from '../../store/authStore';
import Logo from '../../components/Logo';

const { Text } = Typography;

export default function AuthPage() {
  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);

  const submit = async (values: { username: string; password: string }) => {
    setLoading(true);
    setError(null);
    try {
      if (mode === 'login') await login(values.username, values.password);
      else await register(values.username, values.password);
    } catch (err) {
      setError(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-page">
      <Card className="auth-panel">
        <div style={{ marginBottom: 18 }}>
          <Logo size={34} />
          <Text type="secondary" style={{ display: 'block', marginTop: 6 }}>
            研发 · 平台 · 技术支持的智能问题解决助手
          </Text>
        </div>
        <Segmented
          block
          value={mode}
          onChange={(v) => setMode(v as 'login' | 'register')}
          options={[
            { label: '登录', value: 'login' },
            { label: '注册', value: 'register' },
          ]}
          style={{ marginBottom: 18 }}
        />
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 14 }} />}
        <Form layout="vertical" onFinish={submit}>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input autoComplete="username" prefix={<CustomerServiceOutlined />} />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password autoComplete={mode === 'login' ? 'current-password' : 'new-password'} />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading} block>
            {mode === 'login' ? '登录' : '注册'}
          </Button>
        </Form>
      </Card>
    </div>
  );
}
