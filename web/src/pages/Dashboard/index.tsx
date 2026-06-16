// 运营统计看板：总览指标、会话量趋势、学习曲线（点赞/点踩）、热点问题
import { useEffect, useState } from 'react';
import { Card, Col, Row, Statistic, Tag, Typography, message } from 'antd';
import {
  CommentOutlined,
  FileSearchOutlined,
  LikeOutlined,
  RiseOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { api, apiErrorMessage } from '../../api/client';
import type { DailyPoint, HotQuestion, StatsOverview } from '../../types';

const { Title, Text } = Typography;

// 轻量 SVG 柱状趋势图（避免引入重型图表库）
function TrendChart({ points }: { points: DailyPoint[] }) {
  const width = 720;
  const height = 180;
  const pad = 28;
  const maxSessions = Math.max(1, ...points.map((p) => p.sessions));
  const barW = (width - pad * 2) / Math.max(1, points.length);

  return (
    <svg viewBox={`0 0 ${width} ${height + 30}`} style={{ width: '100%' }}>
      {points.map((p, i) => {
        const h = (p.sessions / maxSessions) * height;
        const x = pad + i * barW;
        return (
          <g key={p.date}>
            <rect
              x={x + barW * 0.15}
              y={height - h}
              width={barW * 0.7}
              height={Math.max(h, p.sessions > 0 ? 3 : 0)}
              rx={3}
              fill="var(--color-primary)"
              opacity={0.85}
            />
            {p.sessions > 0 && (
              <text x={x + barW / 2} y={height - h - 5} textAnchor="middle" fontSize="10" fill="#6b7280">
                {p.sessions}
              </text>
            )}
            <text x={x + barW / 2} y={height + 16} textAnchor="middle" fontSize="9" fill="#9ca3af">
              {p.date.slice(5)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}

// 学习曲线：点赞率折线（体现"越用越聪明"）
function LearningCurve({ points }: { points: DailyPoint[] }) {
  const width = 720;
  const height = 160;
  const pad = 28;
  const stepX = (width - pad * 2) / Math.max(1, points.length - 1);

  const rates = points.map((p) => {
    const total = p.up + p.down;
    return total > 0 ? p.up / total : null;
  });

  const coords = rates
    .map((r, i) => (r === null ? null : { x: pad + i * stepX, y: height - r * (height - 20) - 10 }))
    .filter((c): c is { x: number; y: number } => c !== null);

  const path = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x},${c.y}`).join(' ');

  return (
    <svg viewBox={`0 0 ${width} ${height + 30}`} style={{ width: '100%' }}>
      {[0, 0.5, 1].map((r) => (
        <g key={r}>
          <line x1={pad} x2={width - pad} y1={height - r * (height - 20) - 10} y2={height - r * (height - 20) - 10}
            stroke="#e5e7eb" strokeDasharray="4 4" />
          <text x={4} y={height - r * (height - 20) - 6} fontSize="9" fill="#9ca3af">{Math.round(r * 100)}%</text>
        </g>
      ))}
      {coords.length > 1 && <path d={path} fill="none" stroke="var(--color-primary)" strokeWidth={2.5} />}
      {coords.map((c, i) => (
        <circle key={i} cx={c.x} cy={c.y} r={3.5} fill="var(--color-primary)" />
      ))}
      {points.map((p, i) => (
        <text key={p.date} x={pad + i * stepX} y={height + 16} textAnchor="middle" fontSize="9" fill="#9ca3af">
          {p.date.slice(5)}
        </text>
      ))}
      {coords.length === 0 && (
        <text x={width / 2} y={height / 2} textAnchor="middle" fontSize="12" fill="#9ca3af">
          暂无反馈数据
        </text>
      )}
    </svg>
  );
}

export default function DashboardPage() {
  const [overview, setOverview] = useState<StatsOverview | null>(null);
  const [daily, setDaily] = useState<DailyPoint[]>([]);
  const [hot, setHot] = useState<HotQuestion[]>([]);

  useEffect(() => {
    const load = async () => {
      try {
        const [o, d, h] = await Promise.all([
          api.getStatsOverview(),
          api.getStatsDaily(14),
          api.getHotQuestions(),
        ]);
        setOverview(o);
        setDaily(d);
        setHot(h);
      } catch (err) {
        message.error(apiErrorMessage(err));
      }
    };
    load();
    const t = window.setInterval(load, 30000);
    return () => window.clearInterval(t);
  }, []);

  const pct = (v: number) => `${(v * 100).toFixed(1)}%`;

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <Title level={4} style={{ marginTop: 0 }}>运营看板</Title>

      <Row gutter={[16, 16]}>
        <Col xs={12} md={6}>
          <Card><Statistic title="当前活跃会话" value={overview?.activeSessions ?? 0} prefix={<UserOutlined />} /></Card>
        </Col>
        <Col xs={12} md={6}>
          <Card><Statistic title="排队中" value={overview?.queuedSessions ?? 0} prefix={<TeamOutlined />} /></Card>
        </Col>
        <Col xs={12} md={6}>
          <Card><Statistic title="今日会话" value={overview?.sessionsToday ?? 0} prefix={<CommentOutlined />} /></Card>
        </Col>
        <Col xs={12} md={6}>
          <Card><Statistic title="近7天会话" value={overview?.sessions7d ?? 0} prefix={<RiseOutlined />} /></Card>
        </Col>
        <Col xs={12} md={6}>
          <Card>
            <Statistic title="满意率（近7天）" value={overview ? pct(overview.satisfactionRate) : '-'}
              prefix={<LikeOutlined />} valueStyle={{ color: '#10b981' }} />
            <Text type="secondary" style={{ fontSize: 12 }}>
              赞 {overview?.feedbackUp7d ?? 0} / 踩 {overview?.feedbackDown7d ?? 0}
            </Text>
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card>
            <Statistic title="知识命中率（近7天）" value={overview ? pct(overview.knowledgeHitRate) : '-'}
              prefix={<FileSearchOutlined />} />
            <Text type="secondary" style={{ fontSize: 12 }}>含知识检索的回答 {overview?.knowledgeHits7d ?? 0} 条</Text>
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card>
            <Statistic title="转人工率（近7天）" value={overview ? pct(overview.handoffRate) : '-'} />
            <Text type="secondary" style={{ fontSize: 12 }}>工单 {overview?.tickets7d ?? 0} 个</Text>
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card><Statistic title="用户消息（近7天）" value={overview?.userMessages7d ?? 0} /></Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col span={24}>
          <Card title="会话量趋势（近14天）"><TrendChart points={daily} /></Card>
        </Col>
        <Col span={24}>
          <Card title="学习曲线 · 每日满意率（自学习闭环效果）"><LearningCurve points={daily} /></Card>
        </Col>
        <Col span={24}>
          <Card title="热点问题关键词（近7天）">
            {hot.length === 0 ? (
              <Text type="secondary">暂无数据</Text>
            ) : (
              hot.map((q) => (
                <Tag key={q.keyword} color="green" style={{ fontSize: 13, padding: '3px 10px', marginBottom: 8 }}>
                  {q.keyword} × {q.count}
                </Tag>
              ))
            )}
          </Card>
        </Col>
      </Row>
    </div>
  );
}
