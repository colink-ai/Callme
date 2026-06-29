-- Release: v0.1.2
-- 领域与领域知识源。领域用于隔离 Agent Runtime、会话和知识库配置。
-- +goose Up
CREATE TABLE IF NOT EXISTS domains (
    id               VARCHAR(64) PRIMARY KEY,
    name             VARCHAR(128) NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    default_agent_id VARCHAR(128) NOT NULL DEFAULT '',
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS domain_knowledge_sources (
    id         VARCHAR(36) PRIMARY KEY,
    domain_id  VARCHAR(64) NOT NULL,
    name       VARCHAR(128) NOT NULL,
    type       VARCHAR(16) NOT NULL,
    url        TEXT NOT NULL DEFAULT '',
    headers    TEXT NOT NULL DEFAULT '{}',
    command    TEXT NOT NULL DEFAULT '',
    args       TEXT NOT NULL DEFAULT '[]',
    env        TEXT NOT NULL DEFAULT '{}',
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_domain_sources_domain ON domain_knowledge_sources(domain_id);
CREATE INDEX IF NOT EXISTS idx_domain_sources_enabled ON domain_knowledge_sources(enabled);

ALTER TABLE sessions ADD COLUMN domain_id VARCHAR(64) NOT NULL DEFAULT 'default';
CREATE INDEX IF NOT EXISTS idx_sessions_domain ON sessions(domain_id);

INSERT OR IGNORE INTO domains (id, name, description, enabled, created_at, updated_at)
VALUES ('default', '默认领域', '默认领域，兼容既有会话和知识配置。', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- +goose Down
DROP INDEX IF EXISTS idx_sessions_domain;
DROP INDEX IF EXISTS idx_domain_sources_enabled;
DROP INDEX IF EXISTS idx_domain_sources_domain;
DROP TABLE IF EXISTS domain_knowledge_sources;
DROP TABLE IF EXISTS domains;
