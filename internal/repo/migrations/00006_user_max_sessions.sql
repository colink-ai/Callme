-- Release: v0.1.1
-- 用户个人并发上限：普通用户默认 1，VIP 默认 2，管理员默认 10，可在用户管理中调整。
-- +goose Up
ALTER TABLE users ADD COLUMN max_sessions INTEGER NOT NULL DEFAULT 1;
UPDATE users
SET max_sessions = CASE
    WHEN role = 'admin' OR roles LIKE '%"admin"%' THEN 10
    WHEN role = 'vip' OR roles LIKE '%"vip"%' THEN 2
    ELSE 1
END
WHERE max_sessions IS NULL OR max_sessions <= 1;

-- +goose Down
UPDATE users SET max_sessions = 1;
