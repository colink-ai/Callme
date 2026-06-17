-- Release: v0.1.1
-- AI 学习任务执行历史：记录每次自动挖掘历史会话并生成候选知识的运行情况。
-- +goose Up
CREATE TABLE IF NOT EXISTS learning_jobs (
    id             VARCHAR(36) PRIMARY KEY,
    source         VARCHAR(32) NOT NULL,
    status         VARCHAR(16) NOT NULL,
    input_sessions INTEGER NOT NULL DEFAULT 0,
    output_assets  INTEGER NOT NULL DEFAULT 0,
    error          TEXT,
    started_at     TIMESTAMP NOT NULL,
    finished_at    TIMESTAMP NULL
);
CREATE INDEX IF NOT EXISTS idx_learning_jobs_started ON learning_jobs(started_at);
CREATE INDEX IF NOT EXISTS idx_learning_jobs_status ON learning_jobs(status);

-- +goose Down
DROP TABLE IF EXISTS learning_jobs;
