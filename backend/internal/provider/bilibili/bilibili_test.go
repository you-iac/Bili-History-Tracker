package bilibili

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const testBVID = "BV17x411w7KC"

const successBody = `{"code":0,"data":{"bvid":"BV17x411w7KC","title":"测试视频","desc":"简介","pic":"https://example.com/cover.jpg","pubdate":1700000000,"duration":42,"owner":{"mid":12345,"name":"测试UP"}}}`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func withResponse(status int, body string) *Provider {
	p := New()
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	return p
}

func TestGetVideo(t *testing.T) {
	p := New()
	requests := 0
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.bilibili.com" ||
			r.URL.Path != "/x/web-interface/view" || r.URL.Query().Get("bvid") != testBVID {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(successBody)), Header: make(http.Header), Request: r}, nil
	})
	before := time.Now().UTC()
	video, err := p.GetVideo(context.Background(), testBVID)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || video.Platform != "bilibili" || video.ID != testBVID || video.Title != "测试视频" ||
		video.Description != "简介" || video.Creator.ID != "12345" || video.Creator.Name != "测试UP" ||
		video.DurationSeconds != 42 || video.PublishedAt == nil || video.PublishedAt.Unix() != 1700000000 ||
		video.URL != "https://www.bilibili.com/video/"+testBVID || video.CoverURL != "https://example.com/cover.jpg" ||
		video.SourceURL != videoEndpoint+"?bvid="+testBVID || video.ObservedAt.Before(before) || video.ObservedAt.After(time.Now()) {
		t.Fatalf("unexpected normalized video: %+v", video)
	}
}

func TestInvalidIDsDoNotMakeRequests(t *testing.T) {
	p := New()
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid ID must not reach upstream")
		return nil, nil
	})
	for _, id := range []string{"", "av123", "https://www.bilibili.com/video/" + testBVID, "BV17x411w7K0", testBVID + "?x=y"} {
		t.Run(id, func(t *testing.T) {
			_, err := p.GetVideo(context.Background(), id)
			assertKind(t, err, source.InvalidInput)
		})
	}
}

func TestUpstreamFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   source.ErrorKind
		code   int
	}{
		{"permission", 403, "private response", source.AccessRestricted, 0},
		{"risk control", 412, "<html>blocked</html>", source.AccessRestricted, 0},
		{"rate limit", 429, "", source.RateLimited, 0},
		{"server failure", 503, "", source.UpstreamFailure, 0},
		{"missing endpoint", 404, "", source.UpstreamFailure, 0},
		{"redirect", 302, "", source.UpstreamFailure, 0},
		{"not found", 200, `{"code":-404}`, source.NotFound, -404},
		{"not logged in", 200, `{"code":-101}`, source.AccessRestricted, -101},
		{"not visible", 200, `{"code":62002}`, source.AccessRestricted, 62002},
		{"API rate limit", 200, `{"code":-509}`, source.RateLimited, -509},
		{"unknown code", 200, `{"code":123456}`, source.UpstreamFailure, 123456},
		{"HTML", 200, "<html>blocked</html>", source.InvalidResponse, 0},
		{"missing code", 200, `{"data":{}}`, source.InvalidResponse, 0},
		{"null code", 200, `{"code":null}`, source.InvalidResponse, 0},
		{"missing data", 200, `{"code":0}`, source.InvalidResponse, 0},
		{"null data", 200, `{"code":0,"data":null}`, source.InvalidResponse, 0},
		{"empty data", 200, `{"code":0,"data":{}}`, source.InvalidResponse, 0},
		{"wrong video", 200, strings.Replace(successBody, testBVID, "BV1Q541167Qg", 1), source.InvalidResponse, 0},
		{"negative duration", 200, strings.Replace(successBody, `"duration":42`, `"duration":-1`, 1), source.InvalidResponse, 0},
		{"trailing JSON", 200, successBody + `{}`, source.InvalidResponse, 0},
		{"oversize body", 200, strings.Repeat(" ", maxResponseSize+1), source.InvalidResponse, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := withResponse(tt.status, tt.body).GetVideo(context.Background(), testBVID)
			queryError := assertKind(t, err, tt.kind)
			if queryError.UpstreamCode != tt.code {
				t.Fatalf("upstream code = %d, want %d", queryError.UpstreamCode, tt.code)
			}
		})
	}
}

func TestMissingPublicationTimeStaysUnknown(t *testing.T) {
	body := strings.Replace(successBody, `"pubdate":1700000000,`, "", 1)
	video, err := withResponse(200, body).GetVideo(context.Background(), testBVID)
	if err != nil || video.PublishedAt != nil {
		t.Fatalf("expected unknown publication time, got %+v, %v", video.PublishedAt, err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	p := New()
	requests := 0
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests > 1 {
			t.Fatal("provider followed an upstream redirect")
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://example.com/other-source"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})
	_, err := p.GetVideo(context.Background(), testBVID)
	assertKind(t, err, source.UpstreamFailure)
}

func TestCancellationAndTransportErrors(t *testing.T) {
	p := New()
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.GetVideo(ctx, testBVID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	_, err = p.GetVideo(context.Background(), testBVID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingBody) Close() error             { return nil }

func TestReadFailure(t *testing.T) {
	p := New()
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: failingBody{}, Header: make(http.Header), Request: r}, nil
	})
	_, err := p.GetVideo(context.Background(), testBVID)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error lost: %v", err)
	}
}

func assertKind(t *testing.T, err error, kind source.ErrorKind) *source.Error {
	t.Helper()
	var queryError *source.Error
	if !errors.As(err, &queryError) || queryError.Kind != kind {
		t.Fatalf("error = %v, want kind %s", err, kind)
	}
	if strings.Contains(fmt.Sprint(err), "private response") {
		t.Fatal("upstream body leaked into error")
	}
	return queryError
}
