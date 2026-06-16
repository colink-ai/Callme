// Package repo 数据访问层（SQLite）
package repo

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// migrationsFS 嵌入式 SQL 迁移文件（随二进制发布，单产物部署不丢迁移）。
// 新增 DB 变更：在 migrations/ 下加 NNNNN_描述.sql（带 -- +goose Up/Down），
// 文件头注明所属发布版本（-- Release: vX.Y.Z），切勿修改已发布文件。
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations"

// Open 打开 SQLite 数据库并执行 goose 版本化迁移
func Open(driver, dsn string) (*sql.DB, error) {
	if driver != "" && driver != "sqlite" {
		return nil, fmt.Errorf("仅支持 sqlite 数据库, got: %s", driver)
	}
	if dir := filepath.Dir(dsn); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	// modernc.org/sqlite 注册的驱动名为 sqlite
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// SQLite 写并发受限，串行化连接避免 SQLITE_BUSY
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate 用 goose 应用 migrations/ 下所有未执行的迁移。
// 启动时同步执行；只跑 goose_db_version 里没有的版本；每条在事务内原子执行。
// 旧库（已有表、无 goose_db_version）会因初始迁移使用 IF NOT EXISTS 而被安全采纳为版本 1。
func migrate(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("goose migrate: %w", err)
	}
	return nil
}

// SchemaVersion 返回当前已应用的最高 schema 版本（用于启动日志 / 版本接口）。
func SchemaVersion(db *sql.DB) int {
	v, err := goose.GetDBVersion(db)
	if err != nil {
		return 0
	}
	return int(v)
}
