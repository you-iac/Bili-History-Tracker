// client_test.go 验证旧服务器兼容提示、基地址边界和重定向拒绝。
package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBaseAndServerCompatibility(t *testing.T) {
	for _, base := range []string{"file:///tmp/test", "https://u:p@example.com", "http://example.com?token=secret", "http://example.com/#x", "http://example.com/a/../b"} {
		if _, err := NormalizeBase(base); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	base, err := NormalizeBase(" http://example.com/prefix/ ")
	if err != nil || base != "http://example.com/prefix" {
		t.Fatal(base, err)
	}
	for _, mode := range []string{"html", "redirect", "wrong-shape"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, "/other", 302)
					return
				}
				if mode == "html" {
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte("<html>old backend</html>"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"items":[]}`))
			}))
			defer server.Close()
			if _, err := New(nil).Status(context.Background(), server.URL, "video", "BV17x411w7KC"); err == nil {
				t.Fatal("incompatible response accepted")
			}
		})
	}
}
