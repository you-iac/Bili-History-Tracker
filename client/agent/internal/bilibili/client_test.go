// client_test.go 使用固定上游响应验证签名、凭据边界、合作视频和风控响应。
package bilibili

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const img = "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png"
const sub = "https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestWBIFixedVector(t *testing.T) {
	keys, err := NewKeys(img, sub)
	if err != nil {
		t.Fatal(err)
	}
	if keys.mixin != "ea1db124af3c7062474693fa704f4ff8" {
		t.Fatal(keys.mixin)
	}
	query := url.Values{"foo": {"114"}, "bar": {"514"}, "zab": {"1919810"}}
	signed := keys.Sign(query, time.Unix(1702204169, 0))
	if signed.Get("w_rid") != "8f6f2b5b3d485fe1886cec6a0be8c5d4" {
		t.Fatal(signed)
	}
	if query.Has("wts") || query.Has("w_rid") {
		t.Fatal("mutated input")
	}
	if _, err := NewKeys("https://example.com/not-a-key.png", sub); err == nil {
		t.Fatal("accepted malformed key")
	}
}
func TestCookieAndUIDValidation(t *testing.T) {
	valid, err := NormalizeCookie("Cookie: SESSDATA=fake%2Ctest; empty=; DedeUserID=1")
	if err != nil || valid != "SESSDATA=fake%2Ctest; empty=; DedeUserID=1" {
		t.Fatal(valid, err)
	}
	for _, input := range []string{"a=x", "SESSDATA=", "SESSDATA=secret\r\nX-Injected: true", "SESSDATA=secret; SESSDATA=other"} {
		if _, err := NormalizeCookie(input); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe input accepted/reflected: %q", input)
		}
	}
	for _, input := range []string{"0", "01", "-1", "1e3", "https://evil.invalid/123", "https://space.bilibili.com.evil.invalid/123", "https://space.bilibili.com/123/video"} {
		if _, err := CreatorID(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if uid, err := CreatorID("https://space.bilibili.com/122541/?spm_id_from=333"); err != nil || uid != "122541" {
		t.Fatal(uid, err)
	}
}
func TestSignedRequestAndCollaborationVideo(t *testing.T) {
	keys, _ := NewKeys(img, sub)
	client := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.bilibili.com" || r.URL.Path != "/x/space/wbi/arc/search" || r.Header.Get("Cookie") != "SESSDATA=fake" {
			t.Errorf("wrong request %s", r.URL.Host)
		}
		q := r.URL.Query()
		if q.Get("pn") != "5" || q.Get("ps") != "30" || q.Get("mid") != "123" || len(q.Get("w_rid")) != 32 || q.Get("wts") == "" {
			t.Errorf("wrong signed parameters: %v", q)
		}
		return response(`{"code":0,"data":{"page":{"pn":5,"ps":30,"count":121},"list":{"vlist":[{"bvid":"BV1xx411c7mD","title":"合作视频","mid":999,"created":1700000000}]}}}`, 200), nil
	})})
	page, err := client.Videos(context.Background(), "SESSDATA=fake", "123", 5, keys)
	if err != nil || len(page.Videos) != 1 || page.Total != 121 {
		t.Fatal(page, err)
	}
}
func TestRejectsAnonymousAndMalformedResponses(t *testing.T) {
	keys, _ := NewKeys(img, sub)
	for _, body := range []string{
		`{"code":0,"data":{"v_voucher":"challenge"}}`,
		`{"code":0,"data":{"list":{"vlist":[]}}}`,
		`{"code":0,"data":{"page":{"pn":1,"ps":30,"count":1},"list":{"vlist":[{"bvid":"not-bv"}]}}}`,
		`{"data":{"list":{"vlist":[]}}}`,
		`{"code":0,"data":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			c := New(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(body, 200), nil })})
			if _, err := c.Videos(context.Background(), "SESSDATA=fake", "123", 1, keys); err == nil {
				t.Fatal("accepted incomplete response")
			}
		})
	}
	nav := map[string]any{"code": -101, "data": map[string]any{"isLogin": false, "wbi_img": map[string]string{"img_url": img, "sub_url": sub}}}
	encoded, _ := json.Marshal(nav)
	c := New(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(string(encoded), 200), nil })})
	if _, _, err := c.Login(context.Background(), "SESSDATA=expired"); err == nil {
		t.Fatal("anonymous nav treated as authenticated")
	}
}
func TestRedirectCannotLeakCookie(t *testing.T) {
	count := 0
	c := New(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		count++
		r := response("", 302)
		r.Header.Set("Location", "https://elsewhere.invalid/")
		return r, nil
	})})
	_, _, err := c.Login(context.Background(), "SESSDATA=never-forward")
	if err == nil || count != 1 || strings.Contains(err.Error(), "never-forward") {
		t.Fatal(count, err)
	}
}
