// list_page_test.go 验证适配器内部遍历、公开请求、重试分类和可恢复续页。
package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
)

func listBV(i int) string {
	const chars = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	return "BV1oa41137" + string(chars[i/len(chars)]) + string(chars[i%len(chars)])
}
func archiveBody(ids ...int) string {
	items := []map[string]any{}
	for _, id := range ids {
		items = append(items, map[string]any{"bvid": listBV(id), "title": fmt.Sprintf("video %d", id), "pic": "https://example.test/cover.jpg", "pubdate": 1662438000})
	}
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"archives": items}})
	return string(raw)
}
func TestVideoPagesTraverseDirectoriesAndMembersPrivately(t *testing.T) {
	p := New()
	p.cookie = "must-not-leave-process"
	calls := 0
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" {
			t.Fatal("public discovery sent credentials")
		}
		q := r.URL.Query()
		switch r.URL.Path {
		case "/x/polymer/web-space/seasons_series_list":
			switch q.Get("page_num") {
			case "1":
				return jsonResponse(200, `{"code":0,"data":{"items_lists":{"seasons_list":[{"meta":{"season_id":1}}],"series_list":[{"meta":{"series_id":2}}]}}}`), nil
			case "2":
				return jsonResponse(200, `{"code":0,"data":{"items_lists":{"seasons_list":[{"meta":{"season_id":3}}],"series_list":[]}}}`), nil
			case "3":
				return jsonResponse(200, `{"code":0,"data":{"items_lists":{"seasons_list":[],"series_list":[]}}}`), nil
			}
		case "/x/polymer/web-space/seasons_archives_list":
			if q.Get("season_id") == "3" {
				return jsonResponse(200, archiveBody(31)), nil
			}
			if q.Get("page_num") == "2" {
				return jsonResponse(200, archiveBody(0, 30)), nil
			}
			ids := make([]int, 30)
			for i := range ids {
				ids[i] = i
			}
			return jsonResponse(200, archiveBody(ids...)), nil
		case "/x/series/archives":
			return jsonResponse(200, archiveBody(0)), nil
		}
		t.Fatalf("unexpected request: %s", r.URL)
		return nil, nil
	})
	cursor := ""
	seen := map[string]bool{}
	for step := 0; step < 10; step++ {
		page, err := p.GetCreatorVideoPage(context.Background(), "1", cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("adapter returned duplicate", item.ID)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 32 || calls != 7 {
		t.Fatalf("found=%d calls=%d", len(seen), calls)
	}
	if _, err := p.GetCreatorVideoPage(context.Background(), "2", cursor); err == nil {
		t.Fatal("cursor accepted for another creator")
	}
}

func TestDiscoveryRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"temporary", 503, `{}`, 3}, {"access", 412, `{}`, 1}, {"unknown_business", 200, `{"code":-12345}`, 1}, {"invalid_json", 200, `no json`, 1}, {"empty", 200, `{"code":0,"data":{"items_lists":{"seasons_list":[],"series_list":[]}}}`, 1}, {"rate_limit", 429, `{}`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New()
			attempts := 0
			waits := []time.Duration{}
			p.listWait = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() }
			p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				response := jsonResponse(tc.status, tc.body)
				response.Header.Set("Retry-After", "2")
				return response, nil
			})
			_, _ = p.GetCreatorVideoPage(context.Background(), "1", "")
			if attempts != tc.want {
				t.Fatalf("attempts=%d want %d", attempts, tc.want)
			}
			if tc.status == 429 && (len(waits) != 2 || waits[0] < time.Second) {
				t.Fatalf("cooldown missing: %v", waits)
			}
		})
	}
}
func TestDiscoveryRetryCancellationAndRecovery(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	p.listWait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return jsonResponse(503, `{}`), nil })
	_, err := p.GetCreatorVideoPage(ctx, "1", "")
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
	p.listWait = func(context.Context, time.Duration) error { return nil }
	calls = 0
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonResponse(503, `{}`), nil
		}
		return jsonResponse(200, `{"code":0,"data":{"items_lists":{"seasons_list":[],"series_list":[]}}}`), nil
	})
	page, err := p.GetCreatorVideoPage(context.Background(), "1", "")
	if err != nil || page.NextCursor != "" || calls != 2 {
		t.Fatalf("recovery=%+v error=%v calls=%d", page, err, calls)
	}
}

func TestDiscoveryStopsBlockedBusinessAndPreservesLongCooldown(t *testing.T) {
	for _, body := range []string{`{"code":-799}`, `{"code":0,"data":{"items_lists":{}}}`} {
		p := New()
		calls := 0
		p.listWait = func(context.Context, time.Duration) error { return nil }
		p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return jsonResponse(200, body), nil })
		if _, err := p.GetCreatorVideoPage(context.Background(), "1", ""); err == nil || calls != 1 {
			t.Fatalf("invalid response retried: %s calls=%d err=%v", body, calls, err)
		}
	}
	p := New()
	calls := 0
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		response := jsonResponse(429, `{}`)
		response.Header.Set("Retry-After", "120")
		return response, nil
	})
	started := time.Now()
	_, err := p.GetCreatorVideoPage(context.Background(), "1", "")
	var se *source.Error
	if !errors.As(err, &se) || se.RetryAt.Before(started.Add(time.Minute)) || calls != 1 || time.Since(started) > time.Second {
		t.Fatalf("long cooldown lost or waited without budget: %v calls=%d", err, calls)
	}
}
func TestDiscoveryDetectsRepeatedMemberPage(t *testing.T) {
	p := New()
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "seasons_series_list") {
			return jsonResponse(200, `{"code":0,"data":{"items_lists":{"seasons_list":[{"meta":{"season_id":1}}]}}}`), nil
		}
		ids := make([]int, 30)
		for i := range ids {
			ids[i] = i
		}
		return jsonResponse(200, archiveBody(ids...)), nil
	})
	a, err := p.GetCreatorVideoPage(context.Background(), "1", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.GetCreatorVideoPage(context.Background(), "1", a.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.GetCreatorVideoPage(context.Background(), "1", b.NextCursor)
	var se *source.Error
	if !errors.As(err, &se) || se.Kind != source.InvalidResponse {
		t.Fatalf("repeated page error: %v", err)
	}
}
