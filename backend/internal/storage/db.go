// db.go 打开 SQLite 数据库、设置连接级 PRAGMA，并按 user_version 顺序执行迁移。
// 迁移遵循“不可变、每版本一个事务、版本高于程序则拒绝启动”的约定。

package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// schemaVersion 是当前程序期望的 schema 版本，由 migrations 目录中的文件数决定。
const schemaVersion = 3

// Store 是数据库句柄。单写者连接串行化写入，避免 SQLite 的锁竞争。
type Store struct {
	db   *sql.DB
	path string
}

// Open 创建数据目录、打开数据库并迁移到当前 schema 版本。
// path 为 SQLite 文件路径；相对路径按进程工作目录解析。
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if dir := filepath.Dir(abs); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	// Windows absolute paths need the file:/// form; "file:D:/..." is parsed
	// as an authority by SQLite URI handling and fails with "invalid uri authority".
	dsnURL := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(abs)}
	dsn := dsnURL.String() +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_pragma=cache_size(-32000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single writer connection keeps writes serialized in the application,
	// which is what SQLite requires; reads share the same queue for now.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	store := &Store{db: db, path: path}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// Path 返回数据库文件路径，供日志和后台展示。
func (s *Store) Path() string { return s.path }

// DB 暴露底层句柄，供同包测试与其他只读用途使用。
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this program (%d)", version, schemaVersion)
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != schemaVersion {
		return fmt.Errorf("migration files (%d) do not match schemaVersion (%d)", len(names), schemaVersion)
	}
	for i, name := range names {
		next := i + 1
		if next <= version {
			continue
		}
		script, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec("PRAGMA user_version = " + strconv.Itoa(next)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", next, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
