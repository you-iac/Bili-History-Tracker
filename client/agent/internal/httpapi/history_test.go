// history_test.go 验证观看历史路由、参数校验与本地会话保护。
package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

type historyPlatformStub struct{ platformStub }

func (historyPlatformStub) History(context.Context, string, int64, int64, bilibili.Keys) (bilibili.HistoryPage, error) {
	return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
		{BVID: "BV17x411w7KC", AuthorID: "123", AuthorName: "测试作者", ViewedAt: time.Now().Unix()},
		{BVID: "BV1xx411c7mD", AuthorID: "456", ViewedAt: time.Now().Add(-time.Minute).Unix()},
	}}, nil
}

func TestHistoryReflectsLocalFollows(t *testing.T) {
	h := setupWithPlatform(t, historyPlatformStub{})
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=test-only"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := h.call("POST", "/api/history/sync", "{}"); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	for _, target := range []struct{ kind, id, endpoint string }{{"video", "BV17x411w7KC", "videos"}, {"creator", "123", "creators"}} {
		check := func(want bool) {
			t.Helper()
			r := h.call("GET", "/api/history/"+target.endpoint, "")
			var data struct {
				Items []struct {
					Followed bool `json:"followed"`
				} `json:"items"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil || r.Code != 200 || data.Total != 2 || len(data.Items) != 2 || data.Items[0].Followed != want || data.Items[1].Followed {
				t.Fatalf("history local state: %s", r.Body.String())
			}
		}
		checkUnfollowed := func(want int) {
			t.Helper()
			for _, limit := range []string{"12", "20", "30"} {
				r := h.call("GET", "/api/history/"+target.endpoint+"?unfollowed=1&limit="+limit+"&page=99", "")
				var data struct {
					Items []struct {
						Followed bool `json:"followed"`
					} `json:"items"`
					Total int `json:"total"`
					Page  int `json:"page"`
				}
				if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil || r.Code != 200 || data.Total != want || len(data.Items) != want || data.Page != 1 {
					t.Fatalf("unfollowed history: %s", r.Body.String())
				}
				for _, item := range data.Items {
					if item.Followed {
						t.Fatal("followed item remained in filtered history")
					}
				}
			}
		}
		check(false)
		checkUnfollowed(2)
		if _, err := h.s.library.Add(target.kind, target.id); err != nil {
			t.Fatal(err)
		}
		check(true)
		checkUnfollowed(1)
		if r := h.call("DELETE", "/api/follows/"+target.kind+"/"+target.id, "{}"); r.Code != 200 {
			t.Fatal(r.Body.String())
		}
		check(false)
		checkUnfollowed(2)
	}
}

func TestHistoryRoutes(t *testing.T) {
	h := setup(t)
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=test-only"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := h.call("POST", "/api/history/sync", "{}"); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, path := range []string{
		"/api/history/videos?days=3&limit=30",
		"/api/history/videos?days=30",
		"/api/history/creators?days=7&limit=20",
		"/api/history/creators?unfollowed=0&limit=12",
	} {
		if r := h.call("GET", path, ""); r.Code != 200 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{
		"/api/history/videos?days=1",
		"/api/history/videos?limit=100",
		"/api/history/creators?days=abc",
		"/api/history/videos?unfollowed=true",
		"/api/history/creators?unfollowed=2",
	} {
		if r := h.call("GET", path, ""); r.Code != 400 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
}
