// migration_test.go 验证删除过滤规则版本列时兼容旧 schema，且不改写历史内容或指纹。
package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateDropsVideoSnapshotVersion(t *testing.T) {
	for _, initial := range []struct {
		name    string
		version int
		filter  int
		payload string
	}{
		{"schema 1", 1, 0, `{"bvid":"BV17x411w7KC","ugc_season":{"id":123}}`},
		{"schema 2 full snapshot", 2, 0, `{"bvid":"BV17x411w7KC","ugc_season":{"id":123}}`},
		{"schema 2 filtered snapshot", 2, 1, `{"bvid":"BV17x411w7KC"}`},
	} {
		t.Run(initial.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			schema, err := migrationsFS.ReadFile("migrations/0001_init.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(string(schema)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`
INSERT INTO creators (platform, creator_id, name, first_seen_at, last_referenced_at)
VALUES ('bilibili', '12345', 'UP', 1, 1);
INSERT INTO videos (platform, video_id, creator_id, title, duration_seconds,
    first_seen_at, last_observed_at, current_hash, current_seq)
VALUES ('bilibili', 'BV17x411w7KC', '12345', 'video', 42, 1, 1, X'0102030405060708', 1);
INSERT INTO video_versions (platform, video_id, seq, observed_at, source_url,
    content_hash, hash_version, payload)
VALUES ('bilibili', 'BV17x411w7KC', 1, 1, 'source', X'0102030405060708', 1, ?);
PRAGMA user_version = 1;`, initial.payload); err != nil {
				t.Fatal(err)
			}
			if initial.version == 2 {
				migration, err := migrationsFS.ReadFile("migrations/0002_video_snapshot_version.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(string(migration)); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE video_versions SET snapshot_version = ?;
PRAGMA user_version = 2;`, initial.filter); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			// 两次打开验证迁移幂等，以及移除标记后新旧快照都可正常读取。
			for range 2 {
				store, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				var version, columnCount, hashVersion, seq int
				var storedPayload, storedHash string
				if err := store.DB().QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
					t.Fatalf("schema version = %d, err=%v", version, err)
				}
				if err := store.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('video_versions')
WHERE name = 'snapshot_version'`).Scan(&columnCount); err != nil || columnCount != 0 {
					t.Fatalf("filter version column still exists: count=%d, err=%v", columnCount, err)
				}
				if err := store.DB().QueryRow(`SELECT payload, hash_version, seq,
    hex(content_hash) FROM video_versions`).Scan(&storedPayload, &hashVersion, &seq, &storedHash); err != nil {
					t.Fatal(err)
				}
				if storedPayload != initial.payload || hashVersion != 1 || seq != 1 || storedHash != "0102030405060708" {
					t.Fatalf("migration changed legacy observation: payload=%s hash=%s/v%d seq=%d", storedPayload, storedHash, hashVersion, seq)
				}
				detail, err := store.LatestVideo(context.Background(), "bilibili", "BV17x411w7KC")
				if err != nil || string(detail.Payload) != initial.payload || detail.CurrentSeq != 1 {
					t.Fatalf("migrated detail is unreadable: %+v, err=%v", detail, err)
				}
				assertInvariants(t, store)
				var integrity string
				if err := store.DB().QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
					t.Fatalf("database integrity: %s, err=%v", integrity, err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
