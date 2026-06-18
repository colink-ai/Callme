import { Button, Card, Empty, Space, Tag, Typography } from 'antd';
import { CloseOutlined, RobotOutlined } from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useAuthStore } from '../store/authStore';
import { useAITaskStore, type AITaskStatus } from '../store/aiTaskStore';

const { Text } = Typography;

const statusMeta: Record<AITaskStatus, { label: string; color: string }> = {
  running: { label: '运行中', color: 'processing' },
  succeeded: { label: '已完成', color: 'green' },
  failed: { label: '失败', color: 'red' },
};

const aiTaskRoles = new Set(['knowledge_staff', 'knowledge_expert', 'admin']);

export default function AITaskPanel() {
  const { user, activeRole } = useAuthStore();
  const { tasks, activeTaskId, panelOpen, setActiveTask, setPanelOpen } = useAITaskStore();
  const roles = user?.roles?.length ? user.roles : user ? [user.role] : [];
  const usingRole = activeRole && roles.includes(activeRole as typeof roles[number]) ? activeRole : user?.role;
  if (!usingRole || !aiTaskRoles.has(usingRole)) return null;

  const runningCount = tasks.filter((task) => task.status === 'running').length;
  const activeTask = tasks.find((task) => task.id === activeTaskId) || tasks[0];

  if (!panelOpen) {
    return (
      <Button
        className="ai-task-float-trigger"
        type="primary"
        icon={<RobotOutlined />}
        onClick={() => setPanelOpen(true)}
      >
        AI 任务{runningCount ? ` ${runningCount}` : ''}
      </Button>
    );
  }

  return (
    <Card
      className="ai-task-panel"
      size="small"
      title={(
        <Space size={8}>
          <RobotOutlined />
          <span>AI 任务</span>
          {runningCount > 0 && <Tag color="processing">{runningCount} 个运行中</Tag>}
        </Space>
      )}
      extra={<Button type="text" size="small" icon={<CloseOutlined />} onClick={() => setPanelOpen(false)} />}
    >
      {tasks.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无 AI 任务" />
      ) : (
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <div className="ai-task-list">
            {tasks.map((task) => {
              const meta = statusMeta[task.status];
              return (
                <button
                  key={task.id}
                  type="button"
                  className={`ai-task-list-item ${activeTask?.id === task.id ? 'active' : ''}`}
                  onClick={() => setActiveTask(task.id)}
                >
                  <span className="ai-task-list-title">{task.title}</span>
                  <Tag color={meta.color} style={{ margin: 0 }}>{meta.label}</Tag>
                </button>
              );
            })}
          </div>
          {activeTask && (
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              <Space wrap>
                <Text strong>{activeTask.title}</Text>
                <Tag>{activeTask.source}</Tag>
                <Tag color={statusMeta[activeTask.status].color}>{statusMeta[activeTask.status].label}</Tag>
              </Space>
              <div className={`ai-task-output markdown-body ${activeTask.status === 'running' ? 'streaming-cursor' : ''}`}>
                {activeTask.content ? (
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{activeTask.content}</ReactMarkdown>
                ) : (
                  <Text type="secondary">等待 AI 输出…</Text>
                )}
              </div>
              {activeTask.error && <Text type="danger">{activeTask.error}</Text>}
            </Space>
          )}
        </Space>
      )}
    </Card>
  );
}
