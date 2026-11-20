// detail_sources_test.go 验证新资料入口继承会话保护，各分支不访问旧详情或提交任务。
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/library"
	"bili-history-tracker/client/agent/internal/remote"
)

type currentStub struct{}

func (currentStub) Current(_ context.Context, k, id string) (bilibili.Metadata, error) {
	return bilibili.Metadata{Kind: k, ID: id, Name: "platform name", ObservedAt: time.Now().UnixMilli()}, nil
}
func TestIndependentLocalDetailEndpoints(t *testing.T) {
	h := setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/detail/creator/123" || r.Method != "GET" {
			t.Error("unexpected server request", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		w.Write([]byte(`{"record":{"kind":"creator","id":"123","seq":0,"recorded":false},"target":{"kind":"creator","id":"123","recorded":false,"tracking":false,"tracking_state":"absent","status":"not_recorded"},"metrics":{"points":[],"truncated":false}}`))
	}))
	defer server.Close()
	collection, err := library.New(h.s.store, remote.New(nil), currentStub{})
	if err != nil {
		t.Fatal(err)
	}
	h.s.library = collection
	if err = collection.Configure(server.URL); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/current/creator/123/cache", "/api/current/creator/123", "/api/history/creator/123?days=7", "/detail-sources.mjs"} {
		r := h.call("GET", path, "")
		if r.Code != 200 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	r := h.call("GET", "/api/current/creator/123", "")
	var v library.CurrentView
	if json.Unmarshal(r.Body.Bytes(), &v) != nil || !v.Available || v.Source != "platform" || v.Item.Seq != 0 {
		t.Fatal(r.Body.String())
	}
	for _, path := range []string{"/api/current/creator/0", "/api/history/creator/123?days=bogus", "/api/history/creator/123/versions?before=0", "/api/history/creator/123/snapshot?seq=-1"} {
		if r := h.call("GET", path, ""); r.Code != 400 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	h.cookie = nil
	if r := h.call("GET", "/api/current/creator/123/cache", ""); r.Code != 401 {
		t.Fatal("unprotected current cache", r.Code)
	}
}
