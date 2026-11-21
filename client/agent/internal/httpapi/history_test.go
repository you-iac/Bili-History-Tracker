// history_test.go 验证观看历史路由、参数校验与本地会话保护。
package httpapi

import "testing"

func TestHistoryRoutes(t *testing.T) {
	h := setup(t)
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=test-only"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := h.call("POST", "/api/history/sync", "{}"); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, path := range []string{
		"/api/history/videos?days=3&limit=30",
		"/api/history/videos?days=30",
		"/api/history/creators?days=7&limit=20",
	} {
		if r := h.call("GET", path, ""); r.Code != 200 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{
		"/api/history/videos?days=1",
		"/api/history/videos?limit=100",
		"/api/history/creators?days=abc",
	} {
		if r := h.call("GET", path, ""); r.Code != 400 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
}
