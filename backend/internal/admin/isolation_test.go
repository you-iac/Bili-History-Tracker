// isolation_test.go 确认8182只服务管理页，不再提供 Agent 协议或旧 /api 路由。
package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminDoesNotExposeAgentAPI(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	h := s.Handler()
	for _, path := range []string{"/", "/creators", "/videos", "/detail/creator/12345"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Fatal(path, w.Code, w.Header())
		}
	}
	for _, path := range []string{"/api/targets/status?kind=creator&id=12345", "/api/metrics/creator/12345?days=7", "/api/creators/12345/videos", "/api/ingest/jobs/1", "/api/v1/platforms/bilibili/creators/12345"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/targets", strings.NewReader(`{"type":"creator","id":"12345"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 || q.calls.Load() != 0 {
		t.Fatal("admin accepted client JSON", w.Code, q.calls.Load())
	}
}
