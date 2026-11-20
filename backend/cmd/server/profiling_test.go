// profiling_test.go 验证诊断配置、实际 pprof 数据与路由隔离，不连接外部平台。
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/admin"
	"bili-history-tracker/backend/internal/publicapi"
	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

func TestProfilingOptions(t *testing.T) {
	for _, tc := range []struct {
		name, addr, block, mutex string
		wantErr                  bool
	}{
		{name: "disabled"},
		{name: "IPv4", addr: "127.0.0.1:6060", block: "1000000", mutex: "5"},
		{name: "IPv6", addr: "[::1]:6060"},
		{name: "unspecified", addr: "0.0.0.0:6060", wantErr: true},
		{name: "public", addr: "192.168.61.128:6060", wantErr: true},
		{name: "hostname", addr: "localhost:6060", wantErr: true},
		{name: "no host", addr: ":6060", wantErr: true},
		{name: "no port", addr: "127.0.0.1", wantErr: true},
		{name: "bad port", addr: "127.0.0.1:65536", wantErr: true},
		{name: "bad rate", addr: "127.0.0.1:6060", block: "-1", wantErr: true},
		{name: "bad fraction", addr: "127.0.0.1:6060", mutex: "invalid", wantErr: true},
		{name: "disabled with sampling", block: "1", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BHT_PPROF_ADDR", tc.addr)
			t.Setenv("BHT_PPROF_BLOCK_RATE", tc.block)
			t.Setenv("BHT_PPROF_MUTEX_FRACTION", tc.mutex)
			cfg, err := profilingOptions()
			if (err != nil) != tc.wantErr {
				t.Fatalf("options=%+v, err=%v, want error=%v", cfg, err, tc.wantErr)
			}
			if err == nil && tc.addr == "" && newProfilingServer(cfg) != nil {
				t.Fatal("disabled profiling creates a server")
			}
			if err == nil && tc.block == "1000000" && (cfg.blockRate != 1000000 || cfg.mutexFraction != 5) {
				t.Fatalf("sampling configuration lost: %+v", cfg)
			}
		})
	}
}

func TestProfilingHTTP(t *testing.T) {
	s := newProfilingServer(profilingConfig{addr: "127.0.0.1:6060"})
	server := httptest.NewServer(s.Handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/goroutine?debug=1", "/debug/pprof/heap", "/debug/pprof/profile?seconds=1"} {
		t.Run(path, func(t *testing.T) {
			res, err := client.Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			if err != nil || res.StatusCode != 200 || len(data) == 0 {
				t.Fatalf("status=%d, body size=%d, err=%v", res.StatusCode, len(data), err)
			}
			if strings.Contains(path, "heap") || strings.Contains(path, "profile?") {
				compressed, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("not a gzip pprof profile: %v", err)
				}
				defer compressed.Close()
				profile, err := io.ReadAll(compressed)
				if err != nil || len(profile) == 0 {
					t.Fatalf("empty profile: %v", err)
				}
			}
		})
	}
	for _, request := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/debug/pprof/", http.StatusMethodNotAllowed},
		{"GET", "/healthz", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		s.Handler.ServeHTTP(w, httptest.NewRequest(request.method, request.path, nil))
		if w.Code != request.want {
			t.Fatalf("%s %s: %d, want %d", request.method, request.path, w.Code, request.want)
		}
	}
}

type noProfilingQueries struct{}

func (noProfilingQueries) GetVideo(context.Context, string, string) (source.Video, error) {
	return source.Video{}, fmt.Errorf("unexpected upstream call")
}

func (noProfilingQueries) GetCreator(context.Context, string, string) (source.CreatorProfile, error) {
	return source.CreatorProfile{}, fmt.Errorf("unexpected upstream call")
}

func TestProfilingNotExposedByBusinessServers(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	queryLog, err := tracker.NewLog(filepath.Join(dir, "query.log"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer queryLog.Close()
	queries := noProfilingQueries{}
	trk := tracker.New(store, queries, queryLog, "bilibili")
	public, err := publicapi.New(store, trk, queries, "bilibili", publicapi.Config{RequestsPerSecond: 100})
	if err != nil {
		t.Fatal(err)
	}
	management, err := admin.New(store, trk, queryLog, "bilibili")
	if err != nil {
		t.Fatal(err)
	}
	defer management.Close()
	// pprof 的 init 已注册 DefaultServeMux，两个业务入口仍必须使用自己的路由树。
	for name, handler := range map[string]http.Handler{"public": public.Handler(), "admin": management.Handler()} {
		for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/profile?seconds=1"} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s exposes profiling: %s returned %d", name, path, w.Code)
			}
		}
	}
}
