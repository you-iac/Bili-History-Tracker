// details_test.go 验证独立详情的范围白名单、既有协议转发与个人关注状态；只读访问不申请跟踪。
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetailRangesAndLocalFollow(t *testing.T) {
	h := setup(t)
	var ranges []string
	posts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Cookie") != "" {
			t.Error("local cookie forwarded")
		}
		switch r.URL.Path {
		case "/detail/creator/123":
			ranges = append(ranges, r.URL.Query().Get("days"))
			w.Write([]byte(`{"item":{"kind":"creator","id":"123","seq":2},"metrics":{"points":[]},"versions":[],"version_page":1,"version_limit":10,"version_total":2}`))
		case "/api/targets/status":
			json.NewEncoder(w).Encode(map[string]any{"kind": "creator", "id": "123", "tracking": posts > 0})
		case "/targets":
			posts++
			w.Write([]byte(`{"kind":"creator","id":"123","tracking":true}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	config, _ := json.Marshal(map[string]string{"server_url": upstream.URL})
	if result := h.call("PUT", "/api/remote/connection", string(config)); result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	for _, days := range []string{"", "7", "30", "90", "all"} {
		result := h.call("GET", "/api/library/creator/123?days="+days, "")
		var data struct {
			ServerURL string          `json:"server_url"`
			Follow    json.RawMessage `json:"local_follow"`
		}
		if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &data) != nil || data.ServerURL != upstream.URL || len(data.Follow) != 0 {
			t.Fatal(result.Code, result.Body.String())
		}
		want := days
		if want == "" {
			want = "30"
		}
		if ranges[len(ranges)-1] != want {
			t.Fatal("range not forwarded", ranges)
		}
	}
	for _, invalid := range []string{"0", "31", "-1", "forever"} {
		if result := h.call("GET", "/api/library/creator/123?days="+invalid, ""); result.Code != 400 {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	if len(ranges) != 5 || posts != 0 {
		t.Fatal("invalid input queried upstream or view submitted tracking", ranges, posts)
	}
	if result := h.call("POST", "/api/follows", `{"kind":"creator","id":"123"}`); result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	result := h.call("GET", "/api/library/creator/123", "")
	var data struct {
		Follow *struct{ ID string } `json:"local_follow"`
	}
	if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &data) != nil || data.Follow == nil || data.Follow.ID != "123" || posts != 1 {
		t.Fatal("personal follow missing or detail wrote tracking", result.Body.String(), posts)
	}
}
