// metadata_test.go 验证本地资料身份、零/未知数量和Cookie仅送固定平台。
package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestCurrentMetadata(t *testing.T) {
	for _, kind := range []string{"video", "creator"} {
		t.Run(kind, func(t *testing.T) {
			id := "123"
			body := `{"code":0,"data":{"card":{"mid":"123","name":"UP","face":"https://i0.hdslb.com/face.jpg"},"follower":0}}`
			if kind == "video" {
				id = "BV17x411w7KC"
				body = `{"code":0,"data":{"bvid":"BV17x411w7KC","title":"视频","pic":"https://i0.hdslb.com/pic.jpg","pubdate":1700000000,"owner":{"mid":123,"name":"UP"},"stat":{"view":0}}}`
			}
			client := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.bilibili.com" || r.URL.Scheme != "https" || r.Header.Get("Cookie") != "SESSDATA=local" {
					t.Error(r.URL, r.Header)
				}
				return response(body, 200), nil
			})})
			got, err := client.Current(context.Background(), "SESSDATA=local", kind, id)
			if err != nil || got.ID != id || got.Metric == nil || *got.Metric != 0 || got.Cover == "" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestCurrentMetadataRejectsMismatchedIdentity(t *testing.T) {
	client := New(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(`{"code":0,"data":{"card":{"mid":"999","name":"wrong"}}}`, 200), nil
	})})
	_, err := client.Current(context.Background(), "SESSDATA=never-reflect", "creator", "123")
	if err == nil || strings.Contains(err.Error(), "never-reflect") {
		t.Fatal(err)
	}
}
