// library_test.go 验证受会话保护的本地关注与远程读写闭环，Cookie 不是此功能的前置条件。
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLibraryAPIWithoutPlatformCookie(t *testing.T) {
	h := setup(t)
	var submitted atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("local session forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/creators":
			w.Write([]byte(`{"items":[]}`))
		case "/targets":
			submitted.Add(1)
			w.Write([]byte(`{"kind":"creator","id":"123","tracking":true,"status":"tracking"}`))
		case "/api/targets/status":
			w.Write([]byte(`{"kind":"creator","id":"123","recorded":true,"tracking":true,"status":"ok"}`))
		default:
			w.Write([]byte(`{"item":{"kind":"creator","id":"123","name":"远程 UP","metric":42,"seq":1},"versions":[{"seq":1,"name":"远程 UP"}],"metrics":{"points":[]}}`))
		}
	}))
	defer upstream.Close()
	config, _ := json.Marshal(map[string]string{"server_url": upstream.URL})
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/remote/connection", string(config)},
		{"POST", "/api/remote/test", "{}"},
		{"POST", "/api/follows", `{"kind":"creator","id":"123"}`},
		{"POST", "/api/follows/creator/123/submit", "{}"},
		{"POST", "/api/follows/creator/123/refresh", "{}"},
		{"GET", "/api/library/creator/123", ""},
	} {
		r := h.call(tc.method, tc.path, tc.body)
		if r.Code != 200 {
			t.Fatal(tc.path, r.Code, r.Body.String())
		}
	}
	r := h.call("GET", "/api/follows?kind=creator", "")
	if !strings.Contains(r.Body.String(), "远程 UP") || !strings.Contains(r.Body.String(), `"tracking":true`) {
		t.Fatal(r.Body.String())
	}
	if r = h.call("DELETE", "/api/follows/creator/123", "{}"); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if submitted.Load() != 1 {
		t.Fatal("unfollow submitted remote action")
	}
	r = h.call("GET", "/api/follows", "")
	if !strings.Contains(r.Body.String(), `"total":0`) {
		t.Fatal(r.Body.String())
	}
	unauthorized := h.request("POST", "/api/follows", `{"kind":"creator","id":"456"}`)
	unauthorized.Header.Del("X-CSRF-Token")
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, unauthorized)
	if w.Code != 403 {
		t.Fatal("unprotected local follow write", w.Code)
	}
	r = h.call("POST", "/api/follows", `{"kind":"video","id":"https://evil.invalid/video/BV17x411w7KC"}`)
	if r.Code != 400 {
		t.Fatal("accepted arbitrary video URL", r.Code)
	}
}
