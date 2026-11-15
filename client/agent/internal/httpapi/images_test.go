// images_test.go 验证图片地址解析沿用本地会话与来源保护，不返回未受信任地址或代理任意资源。
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageAPIUsesExistingSnapshotAndProtectedSession(t *testing.T) {
	h := setup(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/detail/creator/123" || r.URL.Query().Get("seq") != "1" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected image source", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"item":{"kind":"creator","id":"123","seq":1},"snapshot_seq":1,"snapshot":{"card":{"face":"http://i0.hdslb.com/avatar.jpg"}}}`))
	}))
	defer upstream.Close()
	config, _ := json.Marshal(map[string]string{"server_url": upstream.URL})
	if result := h.call("PUT", "/api/remote/connection", string(config)); result.Code != 200 {
		t.Fatal(result.Code)
	}
	for range 2 {
		result := h.call("GET", "/api/images/creator/123?seq=1", "")
		if result.Code != 200 || !strings.Contains(result.Body.String(), "https://i0.hdslb.com/avatar.jpg") {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("cached image requeried", calls)
	}
	for _, path := range []string{"/api/images/creator/123?seq=-1", "/api/images/video/invalid?seq=1", "/api/images/creator/123?seq=abc"} {
		if result := h.call("GET", path, ""); result.Code != 400 {
			t.Fatal(path, result.Code)
		}
	}
	request := h.request("GET", "/api/images/creator/123?seq=1", "")
	request.Header.Del("Cookie")
	response := httptest.NewRecorder()
	h.s.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("unprotected image metadata", response.Code)
	}
	page := h.call("GET", "/", "")
	policy := page.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "https://*.hdslb.com") || !strings.Contains(policy, "connect-src 'self'") || strings.Contains(policy, "img-src *") {
		t.Fatal("unexpected CSP", policy)
	}
}
