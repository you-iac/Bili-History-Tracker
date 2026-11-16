// main_test.go 验证公共限额的默认值及显式误配置，不允许配置意外关闭保护。
package main

import (
	"os"
	"testing"
)

func TestPublicLimits(t *testing.T) {
	for _, key := range []string{"BHT_API_RPS", "BHT_API_BATCH_MAX"} {
		old, exists := os.LookupEnv(key)
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
		_ = os.Unsetenv(key)
	}
	cfg, err := publicLimits()
	if err != nil || cfg.RequestsPerSecond != 20 || cfg.BatchMax != 10 {
		t.Fatal(cfg, err)
	}
	for _, key := range []string{"BHT_API_RPS", "BHT_API_BATCH_MAX"} {
		for _, raw := range []string{"", "0", "-1", "invalid", "10001"} {
			t.Setenv(key, raw)
			if _, err := publicLimits(); err == nil {
				t.Fatal("invalid limit accepted", key, raw)
			}
		}
		_ = os.Unsetenv(key)
	}
	t.Setenv("BHT_API_RPS", "5")
	t.Setenv("BHT_API_BATCH_MAX", "2")
	cfg, err = publicLimits()
	if err != nil || cfg.RequestsPerSecond != 5 || cfg.BatchMax != 2 {
		t.Fatal(cfg, err)
	}
}
