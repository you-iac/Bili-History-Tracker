// history_test.go 验证历史游标解析、字段校验与 wbi/凭据边界。
package bilibili

import (
	"context"
	"net/http"
	"testing"
)

func TestHistoryParsesCursorAndItems(t *testing.T) {
	keys, err := NewKeys(img, sub)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"code":0,"data":{"cursor":{"max":123,"view_at":1700000000},"list":[
		{"title":"视频一","cover":"http://i0.hdslb.com/a.jpg","author_name":"UP一","author_mid":123,"view_at":1700000000,"progress":10,"duration":100,"history":{"bvid":"BV17x411w7KC"}},
		{"title":"无效","history":{"bvid":"bad"}}
	]}}`
	var seen bool
	client := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/x/web-interface/history/cursor" || r.Header.Get("Cookie") != "SESSDATA=test" {
			t.Error(r.URL, r.Header)
		}
		if r.URL.Query().Get("business") != "archive" || r.URL.Query().Get("w_rid") == "" || r.URL.Query().Get("wts") == "" {
			t.Error(r.URL)
		}
		seen = true
		return response(body, 200), nil
	})})
	page, err := client.History(context.Background(), "SESSDATA=test", 0, 0, keys)
	if err != nil || !seen || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	item := page.Items[0]
	if item.BVID != "BV17x411w7KC" || item.AuthorID != "123" || item.AuthorName != "UP一" || item.ViewedAt != 1700000000 || item.Duration != 100 {
		t.Fatal(item)
	}
	if page.Max != 123 || page.ViewAt != 1700000000 || page.More {
		t.Fatal(page)
	}
}

func TestHistoryEmptyListEndsSync(t *testing.T) {
	keys, _ := NewKeys(img, sub)
	client := New(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(`{"code":0,"data":{"cursor":{"max":0,"view_at":0},"list":[]}}`, 200), nil
	})})
	page, err := client.History(context.Background(), "SESSDATA=test", 0, 0, keys)
	if err != nil || len(page.Items) != 0 || page.More {
		t.Fatal(page, err)
	}
}
