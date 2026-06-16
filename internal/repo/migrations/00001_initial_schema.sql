-- Release: v0.1.0
-- 初始 schema（建表 + 索引）。全部使用 IF NOT EXISTS，对已有库可安全空跑，
-- 便于已存在的旧库被 goose 采纳为版本 1（无需手动 baseline）。
-- +goose Up
CREATE TABLE IF NOT EXISTS sessions (
    id           VARCHAR(36) PRIMARY KEY,
    client_id    VARCHAR(128) NOT NULL,
    user_id      VARCHAR(36) NOT NULL DEFAULT '',
    status       VARCHAR(16) NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    started_at   TIMESTAMP NULL,
    closed_at    TIMESTAMP NULL,
    close_reason VARCHAR(32) NOT NULL DEFAULT '',
    title        VARCHAR(255) NOT NULL DEFAULT '',
    agent_session_id VARCHAR(128) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS users (
    id            VARCHAR(36) PRIMARY KEY,
    username      VARCHAR(64) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          VARCHAR(16) NOT NULL,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_tokens (
    token      VARCHAR(96) PRIMARY KEY,
    user_id    VARCHAR(36) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_user ON auth_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_expires ON auth_tokens(expires_at);

CREATE TABLE IF NOT EXISTS messages (
    id         VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    role       VARCHAR(16) NOT NULL,
    content    TEXT,
    tool_calls TEXT,
    model      VARCHAR(128) NOT NULL DEFAULT '',
    agent_type VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);

CREATE TABLE IF NOT EXISTS feedback (
    id         VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    rating     VARCHAR(8) NOT NULL,
    correction TEXT,
    distilled  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedback_distilled ON feedback(distilled);

CREATE TABLE IF NOT EXISTS tickets (
    id         VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    reason     TEXT,
    transcript TEXT,
    status     VARCHAR(16) NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    k          VARCHAR(64) PRIMARY KEY,
    v          TEXT,
    updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS tickets;
DROP TABLE IF EXISTS feedback;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS sessions;
