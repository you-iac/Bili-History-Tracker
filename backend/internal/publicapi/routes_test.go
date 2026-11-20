// routes_test.go 验证公共路由白名单与旧实时 API 共用限额，不能进入管理处理器。
package publicapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicRoutesExcludeManagementAndHTML(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	h := s.Handler()
	for _, path := range []string{"/", "/creators", "/videos", "/detail/creator/12345", "/api/metrics/creator/12345?days=7"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, request := range []struct{ method, path string }{
		{"GET", "/manage"}, {"GET", "/logs"}, {"GET", "/activity"}, {"GET", "/assets/app.js"},
		{"GET", "/api/creators/12345/videos"}, {"POST", "/api/ingest/videos"},
		{"POST", "/targets/delete"}, {"POST", "/targets/run"}, {"POST", "/targets/interval"},
		{"GET", "/manage/targets/status"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(request.method, request.path, nil))
		if w.Code != 404 {
			t.Fatal(request, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/targets", strings.NewReader("type=creator&id=12345&interval_hours=0.1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 || q.calls.Load() != 0 {
		t.Fatal("management branch leaked", w.Code, q.calls.Load())
	}
}

func TestLegacyQueriesPreservedAndShareRateLimit(t *testing.T) {
	s, _, q := dashboardServer(t)
	q.creator = creatorSuccess
	s.config.RequestsPerSecond = 2
	h := s.Handler()
	for _, path := range []string{"/api/v1/platforms/bilibili/creators/12345", "/healthz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.Contains(path, "platforms") && !strings.Contains(w.Body.String(), `"data":`) {
			t.Fatal("legacy envelope changed", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/platforms/bilibili/videos/BV17x411w7KC", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"RATE_LIMITED"`) || q.calls.Load() != 1 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("legacy query bypassed global gate", w.Code, q.calls.Load())
	}
}
