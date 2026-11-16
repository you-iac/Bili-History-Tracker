// integration_test.go 在两个真实本机 HTTP 监听上验证共享资料与权限隔离，使用隔离数据库和平台桩。
package publicapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bili-history-tracker/backend/internal/admin"
)

func TestPublicAndAdminListenersShareDataWithoutSharingRoutes(t *testing.T) {
	s, store, q := dashboardServer(t)
	q.creator = creatorSuccess
	management, err := admin.New(store, s.tracker, nil, "bilibili")
	if err != nil {
		t.Fatal(err)
	}
	defer management.Close()
	publicServer := httptest.NewServer(s.Handler())
	defer publicServer.Close()
	adminServer := httptest.NewServer(management.Handler())
	defer adminServer.Close()
	client := publicServer.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request := func(base, method, path, contentType, body string) (int, string) {
		t.Helper()
		r, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept", "application/json")
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(payload)
	}
	if code, body := request(publicServer.URL, "POST", "/targets", "application/json", `{"type":"creator","id":"1"}`); code != 200 || !strings.Contains(body, `"tracking":true`) {
		t.Fatal(code, body)
	}
	for _, path := range []string{"/creators?id=1", "/detail/creator/1", "/api/targets/status?kind=creator&id=1", "/api/metrics/creator/1?days=30"} {
		if code, body := request(publicServer.URL, "GET", path, "", ""); code != 200 || !json.Valid([]byte(body)) {
			t.Fatal(path, code, body)
		}
	}
	if code, body := request(adminServer.URL, "GET", "/detail/creator/1", "", ""); code != 200 || !strings.Contains(body, "测试 UP") || !strings.Contains(body, "/manage/metrics/creator/1") {
		t.Fatal("admin lost shared data/chart route", code, body)
	}
	for _, path := range []string{"/manage/metrics/creator/1?days=30", "/manage/targets/status", "/manage/creators/1/videos?scope=recorded"} {
		if code, body := request(adminServer.URL, "GET", path, "", ""); code != 200 || !json.Valid([]byte(body)) {
			t.Fatal("admin private interaction", path, code, body)
		}
		if code, body := request(publicServer.URL, "GET", path, "", ""); code != 404 {
			t.Fatal("private route exposed publicly", path, code, body)
		}
	}
	if code, body := request(adminServer.URL, "POST", "/targets", "application/json", `{"type":"creator","id":"1"}`); code != 415 {
		t.Fatal("management accepted Agent JSON", code, body)
	}
	if code, body := request(publicServer.URL, "POST", "/targets", "application/x-www-form-urlencoded", "type=creator&id=1&interval_hours=0.1"); code != 415 {
		t.Fatal("public port accepted management form", code, body)
	}
	target, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1")
	if err != nil || !exists || target.IntervalSeconds != 10800 || q.calls.Load() != 1 {
		t.Fatal("port isolation changed scheduler or made extra upstream requests", target, q.calls.Load(), err)
	}
}
