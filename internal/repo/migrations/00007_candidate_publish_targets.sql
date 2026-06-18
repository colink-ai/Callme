-- 候选知识发布目标：候选本身统一为知识，审批时可发布到多个目标。
-- +goose Up
ALTER TABLE candidate_assets ADD COLUMN publish_targets TEXT NOT NULL DEFAULT '["local"]';

-- +goose Down
-- SQLite 不支持通用 DROP COLUMN；保持字段无害存在。
SELECT 1;
