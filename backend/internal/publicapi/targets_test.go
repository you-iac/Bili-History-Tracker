// targets_test.go 验证实际存储副作用、截断、错误应答与取消预算，不使用真实上游。
package publicapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
)

func creatorSuccess(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
	if err := ctx.Err(); err != nil {
		return source.CreatorProfile{}, err
	}
	if id == "99999" {
		return source.CreatorProfile{}, errors.New("upstream failure")
	}
	raw, _ := json.Marshal(map[string]any{"card": map[string]any{"mid": id, "name": "测试 UP", "sign": "介绍"}, "follower": 100})
	count := int64(100)
	return source.CreatorProfile{Platform: platform, ID: id, Name: "测试 UP", Raw: raw, FollowerCount: &count, ObservedAt: time.Now()}, nil
}

func postJSON(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/targets", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestBatchPrefixIncludesInvalidPositionsAndDoesNotTouchTail(t *testing.T) {
	s, store, q := dashboardServer(t)
	q.creator = creatorSuccess
	s.config.BatchMax = 2
	w := postJSON(t, s.Handler(), `{"targets":[{"type":"creator","id":"1"},{"type":"creator","id":"bad"},{"type":"creator","id":"2"},"ignored without target validation"]}`)
	var response struct {
		Received, Processed, Ignored int
		Results                      []targetResult
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if response.Received != 4 || response.Processed != 2 || response.Ignored != 2 || len(response.Results) != 2 || !response.Results[0].Tracking || response.Results[0].Result != "created" || !response.Results[0].OK || response.Results[1].HTTPStatus != 400 || response.Results[1].Tracking {
		t.Fatal(w.Body.String())
	}
	for i, result := range response.Results {
		if result.Index != i {
			t.Fatal("lost original index", result)
		}
	}
	if q.calls.Load() != 1 {
		t.Fatal("tail or invalid position queried upstream", q.calls.Load())
	}
	if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "2"); err != nil || exists {
		t.Fatal("ignored tail created target", exists, err)
	}
	if _, err := store.Content(context.Background(), "bilibili", "creator", "2", time.Now()); err == nil {
		t.Fatal("ignored tail saved metadata")
	}
}

func TestBatchDuplicatePausedAndFailedResults(t *testing.T) {
	s, store, q := dashboardServer(t)
	q.creator = creatorSuccess
	seedDashboard(t, store)
	// 暂停与删除是不同状态；只修改本测试刚创建的隔离库。
	db, err := sql.Open("sqlite", q.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE tracking_targets SET enabled=0 WHERE target_type='creator' AND target_id='12345'"); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, s.Handler(), `{"targets":[{"type":"creator","id":"1"},{"type":"creator","id":"1"},{"type":"creator","id":"12345"},{"type":"creator","id":"99999"}]}`)
	var response struct{ Results []targetResult }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Results) != 4 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i, want := range []int{200, 200, 409, 502} {
		if response.Results[i].HTTPStatus != want || response.Results[i].Tracking != (want == 200) {
			t.Fatal(w.Body.String())
		}
	}
	if response.Results[1].Result != "existing" || q.calls.Load() != 2 {
		t.Fatal("duplicate/paused target queried upstream", w.Body.String(), q.calls.Load())
	}
	if _, exists, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "99999"); exists {
		t.Fatal("failed target persisted")
	}
	paused, _, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "12345")
	if paused.Enabled {
		t.Fatal("paused target resumed")
	}
}

func TestBatchCancellationKeepsSuccessAndStopsFollowingQueries(t *testing.T) {
	s, store, q := dashboardServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.creator = func(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
		if id == "2" {
			cancel()
			return source.CreatorProfile{}, ctx.Err()
		}
		return creatorSuccess(ctx, platform, id)
	}
	results := s.submitBatch(ctx, []json.RawMessage{json.RawMessage(`{"type":"creator","id":"1"}`), json.RawMessage(`{"type":"creator","id":"2"}`), json.RawMessage(`{"type":"creator","id":"3"}`)})
	if len(results) != 3 || !results[0].Tracking || results[1].HTTPStatus != 504 || results[2].HTTPStatus != 504 || q.calls.Load() != 2 {
		t.Fatal(results, q.calls.Load())
	}
	if _, exists, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "1"); !exists {
		t.Fatal("successful prefix lost on cancellation")
	}
	if _, exists, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "3"); exists {
		t.Fatal("cancelled item executed")
	}
}

func TestTargetRequestValidationAndBodyLimit(t *testing.T) {
	s, _, q := dashboardServer(t)
	h := s.Handler()
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"targets":[]}`, `{"targets":null}`, `{"targets":{}}`,
		`{"targets":[{}],"type":""}`, `{"targets":[{}],"id":null}`,
		`{"type":"creator","id":"1","interval_hours":0.1}`,
		`{"type":"creator","id":1}`, `{"type":"creator","id":"1"} {}`,
		`{"targets":[{"type":"creator","id":"1"}],"unknown":1}`,
		`{"targets":[{}, invalid ignored tail]}`,
	} {
		if w := postJSON(t, h, body); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := postJSON(t, h, `{"type":"creator","id":"`+strings.Repeat("1", 64<<10)+`"}`); w.Code != 413 {
		t.Fatal("oversized body", w.Code, w.Body.String())
	}
	// 末尾空白也计入总请求大小，不能通过首个合法 JSON 绕过限制。
	if w := postJSON(t, h, `{"type":"creator","id":"1"}`+strings.Repeat(" ", 64<<10)); w.Code != 413 {
		t.Fatal("oversized trailing whitespace", w.Code, w.Body.String())
	}
	for _, headers := range []map[string]string{
		{"Content-Type": "application/x-www-form-urlencoded"},
		{"Content-Type": "application/json", "Origin": "https://evil.example"},
		{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
	} {
		r := httptest.NewRequest("POST", "/targets", strings.NewReader(`{"type":"creator","id":"1"}`))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 && w.Code != 415 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("bad request reached upstream", q.calls.Load())
	}
}

func TestSingleTargetReplyAndStandardLink(t *testing.T) {
	s, _, q := dashboardServer(t)
	q.creator = creatorSuccess
	h := s.Handler()
	w := postJSON(t, h, `{"type":"creator","id":"https://space.bilibili.com/72541187?spm_id_from=test"}`)
	var data map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data) != 9 || data["id"] != "72541187" || data["tracking"] != true || data["result"] != "created" || data["recorded"] != true || data["tracking_state"] != "active" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = postJSON(t, h, `{"type":"creator","id":"72541187"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"result":"existing"`) || q.calls.Load() != 1 {
		t.Fatal("repeat resampled", w.Code, w.Body.String(), q.calls.Load())
	}
}
