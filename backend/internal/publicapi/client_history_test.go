// client_history_test.go 验证批量历史仅为失败ID兜底，历史读取不进入目录/Content。
package publicapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

type narrowHistory struct {
	historyReader
	t         *testing.T
	fallbacks int
}

func (r *narrowHistory) Content(context.Context, string, string, string, time.Time) (storage.ContentItem, error) {
	r.t.Fatal("history entered Content")
	return storage.ContentItem{}, nil
}
func (r *narrowHistory) Catalog(context.Context, string, storage.CatalogFilter, time.Time) (storage.ContentPage, error) {
	r.t.Fatal("history entered Catalog")
	return storage.ContentPage{}, nil
}
func (r *narrowHistory) MetadataBatch(ctx context.Context, p, k string, ids []string) (map[string]storage.ContentItem, error) {
	r.fallbacks++
	return r.historyReader.MetadataBatch(ctx, p, k, ids)
}

func TestBatchHistoryUsesOnlyRequestedFallbacks(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	reader := &narrowHistory{historyReader: s.reader, t: t}
	s.reader = reader
	for _, fallback := range []bool{false, true} {
		body := `{"kind":"creator","ids":["12345","987"],"days":7}`
		if fallback {
			body = `{"kind":"creator","ids":["12345","987"],"days":7,"fallback_ids":["12345"]}`
		}
		w := postPath(t, s, "/api/metrics/batch", body)
		var reply struct {
			OK    bool
			Items []struct {
				ID       string
				Points   []storage.MetricPoint
				Fallback *struct {
					ID         string
					VideoViews *int64 `json:"video_views"`
				}
			}
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || !reply.OK || len(reply.Items) != 2 || reply.Items[1].Points == nil || reply.Items[1].Fallback != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if (reply.Items[0].Fallback != nil) != fallback || reader.fallbacks != map[bool]int{false: 0, true: 1}[fallback] {
			t.Fatal(w.Body.String(), reader.fallbacks)
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("read called platform")
	}
}

func postPath(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestBatchHistoryRejectsInvalidInput(t *testing.T) {
	s, _, _ := dashboardServer(t)
	for _, body := range []string{`{"kind":"creator","ids":[]}`, `{"kind":"creator","ids":["1","1"]}`, `{"kind":"creator","ids":["1"],"fallback_ids":["2"]}`, `{"kind":"creator","ids":["1"],"days":30}`, `{"kind":"video","ids":["bad"]}`} {
		if w := postPath(t, s, "/api/metrics/batch", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
}

func TestNarrowHistoryAndSnapshot(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	s.reader = &narrowHistory{historyReader: s.reader, t: t}
	for _, path := range []string{"/api/history/creator/12345?days=7", "/api/history/video/BV17x411w7KC?seq=1", "/api/history/creator/987", "/api/metadata/video/BV17x411w7KC"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("read called platform")
	}
}
