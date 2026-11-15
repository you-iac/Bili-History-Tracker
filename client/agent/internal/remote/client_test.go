// client_test.go 验证旧服务器兼容提示、基地址边界和重定向拒绝。
package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOverviewUsesExistingCatalogsOnly(t *testing.T) {
	for _, mode := range []string{"populated", "empty", "video-failure"} {
		t.Run(mode, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" || (r.URL.Path != "/creators" && r.URL.Path != "/videos") || r.URL.Query().Get("page") != "1" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected overview request", r.Method, r.URL.String())
				}
				if mode == "video-failure" && r.URL.Path == "/videos" {
					w.WriteHeader(502)
					w.Write([]byte(`{"error":"temporarily unavailable"}`))
					return
				}
				page := Catalog{Items: []Item{}, Page: 1, Limit: 18}
				if mode != "empty" {
					page.Total = 42
					for range 10 {
						page.Items = append(page.Items, Item{ID: "example"})
					}
				}
				json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			data, err := New(nil).Overview(context.Background(), server.URL)
			if len(paths) != 2 || paths[0] != "/creators" || paths[1] != "/videos" {
				t.Fatal("unexpected endpoints", paths)
			}
			if mode == "video-failure" {
				if err == nil || !data.FetchedAt.IsZero() {
					t.Fatal("partial overview accepted", data, err)
				}
				return
			}
			if err != nil || !data.CatalogBased || data.FetchedAt.IsZero() || data.Creators == nil || data.Videos == nil {
				t.Fatal(data, err)
			}
			want, items := int64(42), 10
			if mode == "empty" {
				want, items = 0, 0
			}
			if data.Summary.Creators != want || data.Summary.Videos != want || len(data.Creators) != items || len(data.Videos) != items || data.CreatorLimit != 18 || data.VideoLimit != 18 {
				t.Fatal("totals or previews differ from catalogs", data)
			}
		})
	}
}

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
