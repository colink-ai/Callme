-- Release: v0.1.3
-- 用户可用领域授权。未配置授权的普通用户默认可用 default，管理员可用所有领域。
-- +goose Up
CREATE TABLE IF NOT EXISTS user_domains (
    user_id    VARCHAR(36) NOT NULL,
    domain_id  VARCHAR(64) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, domain_id)
);
CREATE INDEX IF NOT EXISTS idx_user_domains_domain ON user_domains(domain_id);

INSERT OR IGNORE INTO user_domains (user_id, domain_id, created_at)
SELECT id, 'default', CURRENT_TIMESTAMP FROM users;

-- +goose Down
DROP INDEX IF EXISTS idx_user_domains_domain;
DROP TABLE IF EXISTS user_domains;
