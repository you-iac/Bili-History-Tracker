// summaries_test.go 验证只读摘要同步仍受 CSRF 保护，不以公共跟踪状态代替个人关注。
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshPendingFollowSummariesUsesRecordedDataOnly(t *testing.T) {
	h := setup(t)
	reads := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/creators" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected tracking or cookie request", r.Method, r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		reads++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[{"kind":"creator","id":"123","name":"真实 UP 名称","metric":85000,"growth":59,"seq":1}],"page":1,"limit":18,"total":1}`))
	}))
	defer upstream.Close()
	if err := h.s.library.Configure(upstream.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.library.Add("creator", "123"); err != nil {
		t.Fatal(err)
	}
	r := h.request("POST", "/api/follows/refresh?kind=creator", "{}")
	r.Header.Del("X-CSRF-Token")
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, r)
	if w.Code != 403 || reads != 0 {
		t.Fatal("sync bypassed CSRF", w.Code, reads)
	}
	result := h.call("POST", "/api/follows/refresh?kind=creator", "{}")
	if result.Code != 200 || reads != 1 {
		t.Fatal(result.Code, result.Body.String(), reads)
	}
	result = h.call("GET", "/api/follows?kind=creator&sort=popular", "")
	var data struct {
		Items []struct {
			Summary struct {
				Name   string
				Metric *int64
				Growth *int64
			}
			Pending bool `json:"pending_submission"`
		}
	}
	if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &data) != nil || len(data.Items) != 1 || data.Items[0].Summary.Name != "真实 UP 名称" || data.Items[0].Summary.Metric == nil || *data.Items[0].Summary.Growth != 59 || !data.Items[0].Pending {
		t.Fatal(result.Code, result.Body.String())
	}
	if result := h.call("POST", "/api/follows/refresh?kind=invalid", "{}"); result.Code != 400 || reads != 1 {
		t.Fatal(result.Code, result.Body.String())
	}
}
