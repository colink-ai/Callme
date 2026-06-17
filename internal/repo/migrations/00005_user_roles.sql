-- Release: v0.1.1
-- 用户多角色：保留 role 作为兼容主角色，新增 roles(JSON array) 支持同时具备 VIP / 知识专家等角色。
-- +goose Up
ALTER TABLE users ADD COLUMN roles TEXT NOT NULL DEFAULT '[]';
UPDATE users
SET roles = '["' || role || '"]'
WHERE roles = '[]' OR roles = '';

-- +goose Down
UPDATE users
SET role = COALESCE(NULLIF(role, ''), 'normal');
