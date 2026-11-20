// detail_sources_test.go 验证独立接口不请求上游、不写库以及无资料/历史范围契约。
package publicapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

func TestIndependentDetailReadOnly(t *testing.T) {
	s, store, q := dashboardServer(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p := source.CreatorProfile{Platform: "bilibili", ID: "987", Name: "saved", ObservedAt: now, Raw: json.RawMessage(`{"card":{"mid":"987","name":"saved","sign":"description","face":"https://i0.hdslb.com/avatar.jpg"}}`)}
	fp, _ := storage.CreatorFingerprint(p.Raw)
	if _, err := store.SaveCreator(ctx, p, fp); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	store.DB().QueryRow("SELECT total_changes()").Scan(&before)
	h := s.Handler()
	for _, path := range []string{"/api/detail/creator/987", "/api/detail/creator/987/saved", "/api/detail/creator/987/versions", "/api/detail/creator/987/snapshot?seq=1", "/api/detail/creator/999"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var response map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || string(response["ok"]) != "true" {
			t.Fatal(path, w.Code, w.Body.String())
		}
		for _, field := range []string{"version_total", "video_count", "video_views"} {
			if _, ok := response[field]; ok {
				t.Fatal("unexpected heavy field", field)
			}
		}
	}
	for _, path := range []string{"/api/detail/creator/987?days=bad", "/api/detail/creator/987/versions?before=0", "/api/detail/creator/987/snapshot?seq=0", "/api/detail/creator/0001"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/detail/creator/999/saved", nil))
	var response struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 200 || response.OK || response.Code != "DETAIL_NOT_RECORDED" {
		t.Fatal(w.Body.String())
	}
	store.DB().QueryRow("SELECT total_changes()").Scan(&after)
	if before != after || q.calls.Load() != 0 {
		t.Fatal("read caused writes or upstream calls", before, after, q.calls.Load())
	}
}
