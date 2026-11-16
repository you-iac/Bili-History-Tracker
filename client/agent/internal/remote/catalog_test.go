// catalog_test.go 验证目录选项透传且不会转发平台 Cookie。
package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogSortAndOwnerForwarding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/videos" || r.URL.Query().Get("sort") != "hot" || r.URL.Query().Get("owner") != "123" || r.URL.Query().Get("page") != "2" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect catalog request", r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[],"page":2,"limit":18,"total":30}`))
	}))
	defer server.Close()
	if _, err := New(nil).Catalog(context.Background(), server.URL, "video", "", "", 2, CatalogOptions{Sort: "hot", OwnerID: "123"}); err != nil {
		t.Fatal(err)
	}
}
