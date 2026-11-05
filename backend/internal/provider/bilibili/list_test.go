// list_test.go 覆盖合集发现和带 Cookie 时优先空间列表的行为。

package bilibili

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const seasonsBody = `{"code":0,"data":{"items_lists":{"page":{"total":1},"seasons_list":[{"archives":[` +
	`{"bvid":"BV1oa41137pj","title":"A","pubdate":1662438000},` +
	`{"bvid":"BV1Rt4y1j7uP","title":"B","pubdate":1662045996}]}]}}}`

const spaceListBody = `{"code":0,"data":{"list":{"vlist":[` +
	`{"bvid":"BV1es411972g","title":"C","pubdate":1660000000}]},"page":{"count":1}}}`

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestGetCreatorVideosUsesSeasonsWithoutCookie(t *testing.T) {
	p := New()
	p.cookie = ""
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/x/polymer/web-space/seasons_series_list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, seasonsBody), nil
	})
	videos, err := p.GetCreatorVideos(context.Background(), "122541")
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 2 || videos[0].ID != "BV1oa41137pj" || videos[0].PublishedAt == nil {
		t.Fatalf("unexpected videos: %+v", videos)
	}
}

func TestGetCreatorVideosPrefersSpaceWithCookie(t *testing.T) {
	p := New()
	p.cookie = "SESSDATA=test"
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "SESSDATA=test" {
			t.Fatalf("cookie missing: %v", r.Header)
		}
		if r.URL.Path != "/x/space/arc/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, spaceListBody), nil
	})
	videos, err := p.GetCreatorVideos(context.Background(), "122541")
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 1 || videos[0].ID != "BV1es411972g" {
		t.Fatalf("unexpected videos: %+v", videos)
	}
}

func TestGetCreatorVideosFallsBackWhenSpaceIsBlocked(t *testing.T) {
	p := New()
	p.cookie = "SESSDATA=test"
	calls := 0
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/x/space/arc/search" {
			return jsonResponse(http.StatusPreconditionFailed, ""), nil
		}
		return jsonResponse(http.StatusOK, seasonsBody), nil
	})
	videos, err := p.GetCreatorVideos(context.Background(), "122541")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(videos) != 2 {
		t.Fatalf("expected space failure then season fallback, calls=%d videos=%+v", calls, videos)
	}
}
