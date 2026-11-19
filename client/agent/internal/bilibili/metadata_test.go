// metadata_test.go 覆盖公开资料身份、未知计数、固定端点及凭据隔离。
package bilibili

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestCurrentMetadataPublicAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		kind, id, body string
		valid          bool
		known          bool
	}{
		{"creator", "123", `{"card":{"mid":"123","name":"name","sign":"sign","face":"https://i0.hdslb.com/a.jpg"},"follower":0}`, true, true},
		{"creator", "123", `{"card":{"mid":123,"name":"name"}}`, true, false},
		{"creator", "123", `{"card":{"mid":124,"name":"wrong"}}`, false, false},
		{"creator", "123", `{"card":{"mid":123,"name":"name"},"follower":-1}`, false, false},
		{"video", "BV17x411w7KC", `{"bvid":"BV17x411w7KC","title":"video","owner":{"mid":123,"name":"owner"},"stat":{"view":0},"pubdate":1700000000}`, true, true},
		{"video", "BV17x411w7KC", `{"bvid":"BV17x411w7KC","title":"video","owner":{"mid":"123"},"stat":{}}`, true, false},
		{"video", "BV17x411w7KC", `{"bvid":"BV17x411w7KC","title":"video","owner":{"mid":0}}`, false, false},
	} {
		t.Run(tc.kind+tc.body, func(t *testing.T) {
			jar, _ := cookiejar.New(nil)
			endpoint, _ := url.Parse("https://api.bilibili.com")
			jar.SetCookies(endpoint, []*http.Cookie{{Name: "SESSDATA", Value: "test-only-cookie"}})
			c := New(&http.Client{Jar: jar, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Cookie") != "" || r.Header.Values("Cookie") != nil || r.URL.Host != "api.bilibili.com" || r.URL.Scheme != "https" {
					t.Fatal("credential or endpoint boundary", r.URL)
				}
				if tc.kind == "creator" && r.URL.Path != "/x/web-interface/card" || tc.kind == "video" && r.URL.Path != "/x/web-interface/view" {
					t.Fatal(r.URL)
				}
				return response(`{"code":0,"data":`+tc.body+`}`, 200), nil
			})})
			v, err := c.Current(context.Background(), tc.kind, tc.id)
			if (err == nil) != tc.valid {
				t.Fatal(v, err)
			}
			if tc.valid && (v.Kind != tc.kind || v.ID != tc.id || v.ObservedAt < 1 || (v.Metric != nil) != tc.known) {
				t.Fatal(v)
			}
		})
	}
}
