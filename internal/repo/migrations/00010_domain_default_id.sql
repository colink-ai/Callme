-- Release: v0.1.4
-- 统一领域 ID 格式：历史 default 迁移为 domain-default。
-- +goose Up
INSERT OR IGNORE INTO domains (id, name, description, default_agent_id, enabled, created_at, updated_at)
SELECT 'domain-default', name, description, default_agent_id, enabled, created_at, CURRENT_TIMESTAMP
FROM domains WHERE id='default';

UPDATE sessions SET domain_id='domain-default' WHERE domain_id='default';
UPDATE domain_knowledge_sources SET domain_id='domain-default' WHERE domain_id='default';
INSERT OR IGNORE INTO user_domains (user_id, domain_id, created_at)
SELECT user_id, 'domain-default', created_at FROM user_domains WHERE domain_id='default';
DELETE FROM user_domains WHERE domain_id='default';

INSERT OR IGNORE INTO domains (id, name, description, enabled, created_at, updated_at)
VALUES ('domain-default', '默认领域', '默认领域，兼容既有会话和知识配置。', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

DELETE FROM domains WHERE id='default';

-- +goose Down
UPDATE sessions SET domain_id='default' WHERE domain_id='domain-default';
UPDATE domain_knowledge_sources SET domain_id='default' WHERE domain_id='domain-default';
INSERT OR IGNORE INTO user_domains (user_id, domain_id, created_at)
SELECT user_id, 'default', created_at FROM user_domains WHERE domain_id='domain-default';
DELETE FROM user_domains WHERE domain_id='domain-default';

INSERT OR IGNORE INTO domains (id, name, description, default_agent_id, enabled, created_at, updated_at)
SELECT 'default', name, description, default_agent_id, enabled, created_at, CURRENT_TIMESTAMP
FROM domains WHERE id='domain-default';
DELETE FROM domains WHERE id='domain-default';
