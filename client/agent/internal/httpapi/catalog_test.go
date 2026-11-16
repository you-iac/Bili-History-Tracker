// catalog_test.go 覆盖详情投稿只读、UP 隔离、链接搜索及模块 MIME 的 Windows 回归。
package httpapi

import (
	"encoding/json"
	"mime"
	"strings"
	"testing"
	"time"
)

func TestCreatorVideoCatalogDoesNotStartScanAndIsolatesCreator(t *testing.T) {
	h := setup(t)
	if r := h.call("GET", "/api/creator-videos/123?scope=discovered&sort=published", ""); r.Code != 200 || h.service.Result(0, 1).Job != nil {
		t.Fatal("read started job", r.Code, r.Body.String())
	}
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=test-only"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := h.call("POST", "/api/scan", `{"creator_id":"123"}`); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for h.service.Result(0, 1).Job.State == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for _, tc := range []struct {
		uid   string
		total int
	}{{"123", 135}, {"456", 0}} {
		r := h.call("GET", "/api/creator-videos/"+tc.uid+"?scope=discovered&sort=published&page=2", "")
		var data struct {
			Catalog struct {
				Total int
				Items []any
			}
			Job json.RawMessage
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &data) != nil || data.Catalog.Total != tc.total {
			t.Fatal(r.Code, r.Body.String())
		}
		if tc.uid == "456" && string(data.Job) != "null" {
			t.Fatal("another UP scan leaked", r.Body.String())
		}
	}
	for _, path := range []string{"/api/creator-videos/123?scope=bad", "/api/creator-videos/123?scope=followed&sort=sql", "/api/follows?kind=creator&q=https://evil.com/123", "/api/remote/catalog?kind=creator&sort=sql"} {
		if r := h.call("GET", path, ""); r.Code != 400 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	if _, err := h.s.library.Add("creator", "72541187"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"72541187", "https://space.bilibili.com/72541187", "https://space.bilibili.com/72541187?spm_id_from=abc"} {
		r := h.call("GET", "/api/follows?kind=creator&q="+q, "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"total":1`) {
			t.Fatal(q, r.Code, r.Body.String())
		}
	}
}

func TestModuleMIMEOverridesMachineMapping(t *testing.T) {
	original := mime.TypeByExtension(".mjs")
	if original == "" {
		original = "text/javascript"
	}
	defer mime.AddExtensionType(".mjs", original)
	_ = mime.AddExtensionType(".mjs", "text/plain")
	h := setup(t)
	for _, asset := range []string{"app.js", "navigation.js", "library.js", "view.mjs", "trend.mjs", "cards.js", "detail.js", "creator-videos.js", "images.js"} {
		for _, method := range []string{"GET", "HEAD"} {
			r := h.call(method, "/"+asset, "")
			if r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/javascript") || r.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal(asset, method, r.Code, r.Header())
			}
		}
	}
}
