// detail_sources_test.go 拒绝新接口中身份、序号与指标不一致的响应。
package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoryAndVersionResponseValidation(t *testing.T) {
	for _, body := range []string{
		`{"record":{"kind":"creator","id":"other","seq":1,"recorded":true},"target":{"kind":"creator","id":"123","recorded":true},"metrics":{"points":[]}}`,
		`{"record":{"kind":"creator","id":"123","seq":0,"recorded":true},"target":{"kind":"creator","id":"123","recorded":true},"metrics":{"points":[]}}`,
		`{"record":{"kind":"creator","id":"123","seq":1,"recorded":true},"target":{"kind":"creator","id":"123","recorded":true},"metrics":{"points":[{"at":1,"value":-1}]}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}))
		if _, err := New(nil).History(context.Background(), server.URL, "creator", "123", "30"); err == nil {
			t.Fatal("invalid history accepted", body)
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"kind":"creator","id":"123","items":[{"seq":7},{"seq":8}],"has_more":false,"next_before":0}`))
	}))
	defer server.Close()
	if _, err := New(nil).Versions(context.Background(), server.URL, "creator", "123", 0); err == nil {
		t.Fatal("unordered versions accepted")
	}
}
