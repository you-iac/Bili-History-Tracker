// detail_load_test.go 用隔离 SQLite、同一 HTTP 服务对照旧详情、轻量历史和资料回退。
// 仅 BHT_DETAIL_LOAD_CHECK=1 时运行；模拟数据与短窗口结果不能代表真实 VM 容量。
package publicapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestDetailLoadComparison(t *testing.T) {
	if os.Getenv("BHT_DETAIL_LOAD_CHECK") != "1" {
		t.Skip("explicit synthetic load check")
	}
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	day := time.Now().UTC().Truncate(24 * time.Hour).UnixMilli()
	_, err := store.DB().Exec(`WITH RECURSIVE subjects(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM subjects WHERE n<512), days(d) AS (VALUES(0) UNION ALL SELECT d+1 FROM days WHERE d<29)
INSERT INTO metric_samples(platform,subject_type,subject_id,metric,sampled_at,value)
SELECT 'bilibili','creator',CAST(1000000+n AS TEXT),'follower',?-d*86400000,n+d FROM subjects CROSS JOIN days`, day)
	if err != nil {
		t.Fatal(err)
	}
	// 测处理器容量时移除准入 gate；生产路由的共享限流仍由独立回归覆盖。
	mux := http.NewServeMux()
	mux.HandleFunc("GET /detail/creator/12345", func(w http.ResponseWriter, r *http.Request) { s.clientDetail(w, r, "creator", "12345") })
	mux.HandleFunc("GET /api/detail/{type}/{id}", s.detailHistory)
	mux.HandleFunc("GET /api/detail/{type}/{id}/{part}", s.detailSources)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}, Timeout: 6 * time.Second}
	defer client.CloseIdleConnections()
	var reports []map[string]any
	for _, concurrency := range []int{1, 8, 16, 32} {
		for _, scenario := range []string{"legacy", "history", "history_saved"} {
			paths := []string{"/api/detail/creator/12345?days=30"}
			if scenario == "legacy" {
				paths = []string{"/detail/creator/12345?days=30"}
			}
			if scenario == "history_saved" {
				paths = append(paths, "/api/detail/creator/12345/saved")
			}
			var mu sync.Mutex
			var values []float64
			failures := 0
			before := store.DB().Stats()
			start := time.Now()
			deadline := start.Add(time.Second)
			var wg sync.WaitGroup
			for i := 0; i < concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for time.Now().Before(deadline) {
						began := time.Now()
						success := true
						for _, path := range paths {
							resp, e := client.Get(server.URL + path)
							if e != nil {
								success = false
								break
							}
							body, e := io.ReadAll(resp.Body)
							resp.Body.Close()
							var envelope struct {
								OK bool `json:"ok"`
							}
							if e != nil || resp.StatusCode != 200 || json.Unmarshal(body, &envelope) != nil || !envelope.OK {
								success = false
								break
							}
						}
						elapsed := float64(time.Since(began)) / float64(time.Millisecond)
						mu.Lock()
						if success {
							values = append(values, elapsed)
						} else {
							failures++
						}
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			elapsed := time.Since(start)
			after := store.DB().Stats()
			sort.Float64s(values)
			if failures > 0 || len(values) == 0 {
				t.Fatal(scenario, concurrency, "failed requests", failures)
			}
			percentile := func(p float64) float64 { return values[min(len(values)-1, int(float64(len(values)-1)*p))] }
			r := map[string]any{"scenario": scenario, "concurrency": concurrency, "operations": len(values), "seconds_with_drain": elapsed.Seconds(), "goodput": float64(len(values)) / elapsed.Seconds(), "p50_ms": percentile(.50), "p95_ms": percentile(.95), "p99_ms": percentile(.99), "errors": failures, "db_wait_count": after.WaitCount - before.WaitCount, "db_wait_ms": float64(after.WaitDuration-before.WaitDuration) / float64(time.Millisecond)}
			reports = append(reports, r)
			line, _ := json.Marshal(r)
			t.Log(string(line))
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("synthetic reads called platform")
	}
	output := map[string]any{"scope": "local synthetic HTTP handler capacity without admission gate, 512 extra subjects x 30 samples, 1s launch window plus drain, GOMAXPROCS from environment; no Agent/platform timing", "results": reports}
	encoded, _ := json.MarshalIndent(output, "", "  ")
	if path := os.Getenv("BHT_DETAIL_LOAD_REPORT"); path != "" {
		if err = os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println(string(encoded))
}
