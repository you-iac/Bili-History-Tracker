// main_test.go 验证两个入口的监听默认值与独立覆盖，以及公共限额的默认值和显式误配置。
package main

import (
	"os"
	"testing"
)

func TestListenAddresses(t *testing.T) {
	for _, tc := range []struct {
		name, api, admin, wantAPI, wantAdmin string
		unset                                bool
	}{
		{name: "unset defaults", unset: true, wantAPI: "0.0.0.0:8181", wantAdmin: "127.0.0.1:8182"},
		{name: "empty defaults", wantAPI: "0.0.0.0:8181", wantAdmin: "127.0.0.1:8182"},
		{name: "public override", api: "127.0.0.1:9090", wantAPI: "127.0.0.1:9090", wantAdmin: "127.0.0.1:8182"},
		{name: "admin override", admin: "127.0.0.1:9091", wantAPI: "0.0.0.0:8181", wantAdmin: "127.0.0.1:9091"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BHT_ADDR", tc.api)
			t.Setenv("BHT_ADMIN_ADDR", tc.admin)
			if tc.unset {
				for _, key := range []string{"BHT_ADDR", "BHT_ADMIN_ADDR"} {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			api, admin := listenAddresses()
			if api != tc.wantAPI || admin != tc.wantAdmin {
				t.Fatalf("listen addresses = (%q, %q), want (%q, %q)", api, admin, tc.wantAPI, tc.wantAdmin)
			}
		})
	}
}

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
