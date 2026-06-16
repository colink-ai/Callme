package repo

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 全新库：应应用初始迁移并建好所有核心表，schema 版本 >= 1
func TestMigrateFreshDB(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if got := SchemaVersion(db); got < 1 {
		t.Fatalf("schema version = %d, want >= 1", got)
	}
	for _, tbl := range []string{"users", "sessions", "messages", "feedback", "tickets", "settings", "auth_tokens", "goose_db_version"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Fatalf("missing table %s: %v", tbl, err)
		}
	}
}

// 旧库（已有表 + 旧的自研 schema_migrations、无 goose_db_version）：
// 初始迁移用 IF NOT EXISTS，应被安全采纳为版本 1，不丢数据、不报错
func TestMigrateLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	// 模拟已上线的旧库：表已存在（含全部列）+ 旧的自研迁移记录表
	raw.Exec(`CREATE TABLE sessions (id VARCHAR(36) PRIMARY KEY, client_id VARCHAR(128), user_id VARCHAR(36) DEFAULT '', status VARCHAR(16), created_at TIMESTAMP, started_at TIMESTAMP, closed_at TIMESTAMP, close_reason VARCHAR(32) DEFAULT '', title VARCHAR(255) DEFAULT '', agent_session_id VARCHAR(128) DEFAULT '')`)
	raw.Exec(`CREATE TABLE messages (id VARCHAR(36) PRIMARY KEY, session_id VARCHAR(36), role VARCHAR(16), content TEXT, tool_calls TEXT, model VARCHAR(128) DEFAULT '', agent_type VARCHAR(64) DEFAULT '', created_at TIMESTAMP)`)
	raw.Exec(`CREATE TABLE schema_migrations (id INTEGER PRIMARY KEY, app_version VARCHAR(32), name VARCHAR(128), applied_at TIMESTAMP)`)
	raw.Exec(`INSERT INTO schema_migrations VALUES (1,'0.1.0','initial schema','2026-01-01')`)
	raw.Exec(`INSERT INTO sessions (id, client_id, status, created_at) VALUES ('s1','c1','closed','2026-01-01')`)
	raw.Exec(`INSERT INTO messages (id, session_id, role, content, created_at) VALUES ('m1','s1','user','hi','2026-01-01')`)
	raw.Close()

	db, err := Open("sqlite", path)
	if err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	defer db.Close()

	if got := SchemaVersion(db); got < 1 {
		t.Fatalf("legacy schema version = %d, want >= 1", got)
	}
	// 旧数据未丢
	var n int
	db.QueryRow(`SELECT count(*) FROM sessions WHERE id='s1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("legacy data lost: sessions s1 count=%d", n)
	}
	// 表结构可用（含后加列）
	if _, err := db.Exec(`INSERT INTO messages (id, session_id, role, content, model, agent_type, created_at) VALUES ('m2','s1','assistant','hi','glm','hermes','2026-01-02')`); err != nil {
		t.Fatalf("schema unusable after adopt: %v", err)
	}
}

// 幂等：重复 Open 不应重跑迁移或报错
func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idem.db")
	db1, err := Open("sqlite", path)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	v1 := SchemaVersion(db1)
	db1.Close()

	db2, err := Open("sqlite", path)
	if err != nil {
		t.Fatalf("open2 (idempotent) failed: %v", err)
	}
	defer db2.Close()
	if got := SchemaVersion(db2); got != v1 {
		t.Fatalf("version drifted on reopen: %d != %d", got, v1)
	}
}
