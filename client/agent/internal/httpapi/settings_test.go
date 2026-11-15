// settings_test.go 验证工作目录设置同样受会话与CSRF保护，并保留已关注数据。
package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsMigrateAndKeepLocalFollows(t *testing.T) {
	h := setup(t)
	r := h.call("POST", "/api/follows", `{"kind":"creator","id":"123"}`)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"pending_submission":true`) {
		t.Fatal("no server should still save follow", r.Code, r.Body.String())
	}
	if r = h.call("GET", "/api/overview", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"pending":1`) {
		t.Fatal(r.Body.String())
	}
	target := filepath.Join(t.TempDir(), "new-data")
	body, _ := json.Marshal(map[string]string{"data_directory": target})
	request := h.request("PUT", "/api/settings/directory", string(body))
	request.Header.Del("X-CSRF-Token")
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("directory setting unprotected", w.Code)
	}
	r = h.call("PUT", "/api/settings/directory", string(body))
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = h.call("GET", "/api/follows", ""); !strings.Contains(r.Body.String(), `"id":"123"`) {
		t.Fatal("migration lost follow", r.Body.String())
	}
	if r = h.call("GET", "/api/settings", ""); !strings.Contains(r.Body.String(), "new-data") {
		t.Fatal(r.Body.String())
	}
}
