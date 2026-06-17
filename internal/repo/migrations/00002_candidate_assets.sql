-- Release: v0.1.1
-- 自学习沙箱：候选资产池。反馈蒸馏只写这里(pending)，审批通过后才发布到正式知识，
-- 任何内容都不会自动进入生产回答链路。
-- +goose Up
CREATE TABLE IF NOT EXISTS candidate_assets (
    id                 VARCHAR(36) PRIMARY KEY,
    asset_type         VARCHAR(16) NOT NULL,            -- faq | wiki
    title              VARCHAR(255) NOT NULL,
    question           TEXT,                            -- FAQ 标准问法（wiki 可空）
    content            TEXT NOT NULL,                   -- 正文（标准答案 / wiki 内容）
    evidence           TEXT,                            -- 来源证据(JSON)：会话/消息摘要/纠错
    source_session_id  VARCHAR(36) NOT NULL DEFAULT '',
    source_feedback_id VARCHAR(36) NOT NULL DEFAULT '',
    confidence         REAL NOT NULL DEFAULT 0,
    status             VARCHAR(16) NOT NULL DEFAULT 'pending',  -- pending | approved | rejected
    reviewer           VARCHAR(64) NOT NULL DEFAULT '',
    review_note        TEXT,
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_candidate_status ON candidate_assets(status);
CREATE INDEX IF NOT EXISTS idx_candidate_created ON candidate_assets(created_at);

-- +goose Down
DROP TABLE IF EXISTS candidate_assets;
