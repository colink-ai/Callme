-- Release: v0.1.1
-- Hermes 自学习审计轨：记录 skills / memories 的新增、修改、删除，供管理员定期审计。
-- +goose Up
CREATE TABLE IF NOT EXISTS hermes_learning_assets (
    id           VARCHAR(36) PRIMARY KEY,
    asset_type   VARCHAR(16) NOT NULL,   -- skill | memory
    path         TEXT NOT NULL,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    content      TEXT,
    change_type  VARCHAR(16) NOT NULL,   -- new | modified | deleted
    risk_flags   TEXT,                   -- JSON array
    status       VARCHAR(32) NOT NULL DEFAULT 'pending_review',
    reviewer     VARCHAR(64) NOT NULL DEFAULT '',
    review_note  TEXT,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hermes_learning_path_created ON hermes_learning_assets(path, created_at);
CREATE INDEX IF NOT EXISTS idx_hermes_learning_status ON hermes_learning_assets(status);
CREATE INDEX IF NOT EXISTS idx_hermes_learning_created ON hermes_learning_assets(created_at);

-- +goose Down
DROP TABLE IF EXISTS hermes_learning_assets;
