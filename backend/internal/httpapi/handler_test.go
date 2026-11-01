package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bili-history-tracker/backend/internal/source"
)

type queryFunc func(context.Context, string, string) (source.Video, error)

func (f queryFunc) GetVideo(ctx context.Context, platform, id string) (source.Video, error) {
	return f(ctx, platform, id)
}

func TestQueryRoute(t *testing.T) {
	handler := New(queryFunc(func(ctx context.Context, platform, id string) (source.Video, error) {
		if platform != "another-platform" || id != "opaque-id" {
			t.Fatalf("wrong route parameters: %s %s", platform, id)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("query has no deadline")
		}
		return source.Video{Platform: platform, ID: id, Title: "测试"}, nil
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/platforms/another-platform/videos/opaque-id", nil))
	var response struct {
		Data source.Video `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.Data.ID != "opaque-id" || response.Data.Title != "测试" ||
		w.Header().Get("Content-Type") != "application/json; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   source.ErrorKind
	}{
		{"invalid ID", &source.Error{Kind: source.InvalidInput}, 400, source.InvalidInput},
		{"unknown platform", &source.Error{Kind: source.UnsupportedPlatform}, 404, source.UnsupportedPlatform},
		{"not found", &source.Error{Kind: source.NotFound}, 404, source.NotFound},
		{"blocked upstream", &source.Error{Kind: source.AccessRestricted}, 502, source.AccessRestricted},
		{"limited upstream", &source.Error{Kind: source.RateLimited}, 503, source.RateLimited},
		{"invalid response", &source.Error{Kind: source.InvalidResponse}, 502, source.InvalidResponse},
		{"timeout", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 504, source.UpstreamFailure},
		{"cancellation", fmt.Errorf("wrapped: %w", context.Canceled), 408, source.UpstreamFailure},
		{"transport error", fmt.Errorf("private transport details"), 502, source.UpstreamFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := New(queryFunc(func(context.Context, string, string) (source.Video, error) {
				return source.Video{}, tc.err
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/platforms/bilibili/videos/BV17x411w7KC", nil))
			var response struct {
				Error struct {
					Code source.ErrorKind `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || response.Error.Code != tc.code || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("unexpected error response: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestHealthAndMethodHandling(t *testing.T) {
	handler := New(queryFunc(func(context.Context, string, string) (source.Video, error) {
		t.Fatal("health and invalid methods must not query upstream")
		return source.Video{}, nil
	}))
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/healthz", 200},
		{http.MethodPost, "/api/v1/platforms/bilibili/videos/BV17x411w7KC", 405},
		{http.MethodGet, "/missing", 404},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d", tc.method, tc.path, w.Code)
		}
	}
}
