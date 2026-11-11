// server_test.go 验证会话、来源、CSRF 及从 Cookie 配置到多页导出的本地闭环。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/scanner"
)

type platformStub struct{}

func (platformStub) Login(context.Context, string) (bilibili.Account, bilibili.Keys, error) {
	return bilibili.Account{ID: "88", Name: "本地测试账号"}, bilibili.Keys{}, nil
}
func (platformStub) Videos(_ context.Context, _, _ string, pn int, _ bilibili.Keys) (bilibili.Page, error) {
	r := bilibili.Page{Total: 135, Videos: []bilibili.Video{}}
	for i := (pn - 1) * 30; i < min(pn*30, 135); i++ {
		r.Videos = append(r.Videos, bilibili.Video{ID: fmt.Sprintf("BVtest%06d", i), Title: fmt.Sprintf("视频 %d <script>", i), PublishedAt: 1700000000})
	}
	return r, nil
}

type harness struct {
	s       *Server
	service *scanner.Service
	cookie  *http.Cookie
	csrf    string
}

func setup(t *testing.T) *harness {
	t.Helper()
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := scanner.New(platformStub{}, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	s, err := New(service, "127.0.0.1:8183")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{s: s, service: service}
	r := h.call("POST", "/api/session", `{"token":"`+s.BootstrapToken()+`"}`)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	h.cookie = r.Result().Cookies()[0]
	var result struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &result)
	h.csrf = result.CSRF
	return h
}
func (h *harness) request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:8183"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://127.0.0.1:8183")
	r.Header.Set("Content-Type", "application/json")
	if h.cookie != nil {
		r.AddCookie(h.cookie)
	}
	if h.csrf != "" {
		r.Header.Set("X-CSRF-Token", h.csrf)
	}
	return r
}
func (h *harness) call(method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, h.request(method, path, body))
	return w
}

func TestSecurityBoundaries(t *testing.T) {
	h := setup(t)
	if !h.cookie.HttpOnly || h.cookie.SameSite != http.SameSiteStrictMode || h.cookie.Path != "/" {
		t.Fatal("insecure session cookie", h.cookie)
	}
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"wrong-host", func(r *http.Request) { r.Host = "evil.invalid:8183" }, 403},
		{"foreign-origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.invalid") }, 403},
		{"missing-origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"missing-session", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
		{"missing-csrf", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }, 403},
		{"wrong-csrf", func(r *http.Request) { r.Header.Set("X-CSRF-Token", "wrong") }, 403},
		{"form-post", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 403},
		{"same-site-other-origin", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := h.request("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=fake"}`)
			tc.change(r)
			w := httptest.NewRecorder()
			h.s.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	r := h.call("POST", "/api/session", `{"token":"wrong"}`)
	if r.Code != 401 {
		t.Fatal(r.Code)
	}
}
func TestCookieToFullScanAndExport(t *testing.T) {
	h := setup(t)
	r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=fake-never-return"}`)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, path := range []string{"/api/config", "/api/session"} {
		r = h.call("GET", path, "")
		if r.Code != 200 || strings.Contains(r.Body.String(), "fake-never-return") {
			t.Fatal("credential leaked", path)
		}
	}
	request := h.request("POST", "/api/scan", `{"creator_id":"122541"}`)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, request)
	cancel()
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result := h.service.Result(0, 100)
		if result.Job.State == "complete" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	result := h.service.Result(0, 100)
	if result.Job.State != "complete" || result.Job.Count != 135 {
		t.Fatal("scan cancelled with browser request", result.Job)
	}
	r = h.call("GET", "/api/scan?offset=100&limit=50", "")
	var last scanner.Result
	_ = json.Unmarshal(r.Body.Bytes(), &last)
	if r.Code != 200 || len(last.Videos) != 35 || last.HasMore {
		t.Fatal(r.Code, last)
	}
	r = h.call("GET", "/api/scan/export?format=txt", "")
	if r.Code != 200 || len(strings.Fields(r.Body.String())) != 135 || !strings.Contains(r.Header().Get("Content-Disposition"), "complete.txt") {
		t.Fatal(r.Code, r.Header())
	}
	r = h.call("GET", "/api/scan/export?format=json", "")
	if strings.Contains(r.Body.String(), "fake-never-return") || !strings.Contains(r.Body.String(), `"count":135`) {
		t.Fatal("bad JSON export")
	}
	r = h.call("DELETE", "/api/config/cookie", `{}`)
	if r.Code != 200 || h.service.Config().Configured {
		t.Fatal("did not clear cookie")
	}
	if h.service.Result(0, 1).Job.Count != 135 {
		t.Fatal("clearing cookie deleted results")
	}
}
func TestStaticAssetsAndInvalidInputs(t *testing.T) {
	h := setup(t)
	for _, path := range []string{"/", "/app.css", "/app.js", "/view.mjs", "/favicon.svg"} {
		r := h.call("GET", path, "")
		if r.Code != 200 || r.Header().Get("Content-Security-Policy") == "" {
			t.Fatal(path, r.Code)
		}
	}
	if r := h.call("GET", "/assets.go", ""); r.Code != 404 {
		t.Fatal("Go source exposed", r.Code)
	}
	if r := h.call("GET", "/api/scan?limit=9999", ""); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=secret","unexpected":true}`); r.Code != 400 || strings.Contains(r.Body.String(), "secret") {
		t.Fatal("invalid JSON reflected")
	}
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"`+strings.Repeat("x", 70<<10)+`"}`); r.Code != 400 {
		t.Fatal("oversized body accepted", r.Code)
	}
	if r := h.call("POST", "/api/scan", `{"creator_id":"1"}`); r.Code != 422 {
		t.Fatal("scan without cookie", r.Code)
	}
}
