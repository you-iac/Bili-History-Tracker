// log_test.go 验证有界日志恢复、损坏行容忍和恢复后的追加顺序。
package tracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogRestoresRecentEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.log")
	log, err := NewLog(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"first", "second", "third"} {
		log.Add(Entry{Time: time.Now(), Action: "query_video", Message: message})
	}
	if err = log.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("broken JSON\n")
	_ = file.Close()
	restored, err := NewLog(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	entries := restored.Recent(10)
	if len(entries) != 2 || entries[0].Message != "third" || entries[1].Message != "second" {
		t.Fatalf("restored %+v", entries)
	}
	restored.Add(Entry{Message: "fourth"})
	if restored.Recent(1)[0].Message != "fourth" {
		t.Fatal("append after recovery failed")
	}
}
