// store_test.go 验证系统加密、重复替换、清除凭据与扫描原子存储。
package localstate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCookieProtectionAndReplacement(t *testing.T) {
	directory := t.TempDir()
	s, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SESSDATA=fake-private-cookie-1", "SESSDATA=fake-private-cookie-2"} {
		if err := s.SaveCookie(secret); err != nil {
			t.Fatal(err)
		}
		actual, err := s.LoadCookie()
		if err != nil {
			t.Fatal(err)
		}
		if s.PersistentCookie() {
			if actual != secret {
				t.Fatal("protected credential did not round-trip")
			}
			data, err := os.ReadFile(filepath.Join(directory, "cookie.protected"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("plaintext credential persisted")
			}
		} else if actual != "" {
			t.Fatal("unsupported platform persisted a credential")
		}
	}
	if err := s.SaveCookie(""); err != nil {
		t.Fatal(err)
	}
	if value, err := s.LoadCookie(); value != "" || err != nil {
		t.Fatal("credential not cleared", err)
	}
}
func TestScanAtomicReplacement(t *testing.T) {
	directory := t.TempDir()
	s, _ := New(directory)
	for _, count := range []int{30, 135} {
		if err := s.SaveScan(map[string]int{"count": count}); err != nil {
			t.Fatal(err)
		}
		var value map[string]int
		if err := s.LoadScan(&value); err != nil || value["count"] != count {
			t.Fatal(value, err)
		}
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 || entries[0].Name() != "scan.json" {
		t.Fatal("temporary file leaked", entries)
	}
}
