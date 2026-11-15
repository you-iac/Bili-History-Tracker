// directory_test.go 验证目录迁移保留原件、重启指向、并发写入及拒绝覆盖。
package localstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryMigrationAndRestart(t *testing.T) {
	root := t.TempDir()
	anchor := filepath.Join(root, "original")
	target := filepath.Join(root, "new")
	s, err := New(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLibrary(map[string]any{"version": 1, "follows": []string{"123"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveScan(map[string]int{"page": 3}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveCookie("SESSDATA=test-migrate"); err != nil {
		t.Fatal(err)
	}
	if err = s.SwitchDirectory(target); err != nil {
		t.Fatal(err)
	}
	if s.DirectoryInfo().Directory != target {
		t.Fatal(s.DirectoryInfo())
	}
	if _, err = os.Stat(filepath.Join(anchor, "library.json")); err != nil {
		t.Fatal("original not retained", err)
	}
	if err = s.SaveScan(map[string]int{"page": 8}); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(anchor)
	if err != nil {
		t.Fatal(err)
	}
	var scan map[string]int
	if err = reopened.LoadScan(&scan); err != nil || scan["page"] != 8 {
		t.Fatal(scan, err)
	}
	if s.PersistentCookie() {
		cookie, err := reopened.LoadCookie()
		if err != nil || cookie != "SESSDATA=test-migrate" {
			t.Fatal("cookie migration failed", err)
		}
	}
	third := filepath.Join(root, "third")
	if err = reopened.SwitchDirectory(third); err != nil {
		t.Fatal(err)
	}
	reopened, err = New(anchor)
	if err != nil || reopened.DirectoryInfo().Directory != third {
		t.Fatal("second switch not persistent", err)
	}
}
func TestDirectoryRejectsOverwriteAndConcurrentSaves(t *testing.T) {
	root := t.TempDir()
	s, _ := New(filepath.Join(root, "original"))
	target := filepath.Join(root, "occupied")
	_ = os.MkdirAll(target, 0700)
	_ = os.WriteFile(filepath.Join(target, "important"), []byte("keep"), 0600)
	for _, input := range []string{target, "relative", filepath.Join(s.DirectoryInfo().Directory, "child")} {
		if err := s.SwitchDirectory(input); err == nil {
			t.Fatal("unsafe target accepted", input)
		}
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 40; i++ {
			if err := s.SaveScan(map[string]int{"page": i}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	if err := s.SwitchDirectory(filepath.Join(root, "safe")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var scan map[string]int
	if err := s.LoadScan(&scan); err != nil || scan["page"] != 39 {
		t.Fatal(scan, err)
	}
	data, err := os.ReadFile(filepath.Join(s.DirectoryInfo().Directory, "scan.json"))
	if err != nil || !json.Valid(data) {
		t.Fatal("inconsistent copy", err)
	}
	kept, _ := os.ReadFile(filepath.Join(target, "important"))
	if string(kept) != "keep" {
		t.Fatal("existing target overwritten")
	}
}
