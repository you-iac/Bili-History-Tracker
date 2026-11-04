package bilibili

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const creatorSuccessBody = `{"code":0,"data":{"card":{"mid":"122541","name":"测试UP","face":"https://example.com/avatar.jpg","sign":"个人签名"},"follower":1234}}`

func TestGetCreator(t *testing.T) {
	p := New()
	requests := 0
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.bilibili.com" ||
			r.URL.Path != "/x/web-interface/card" || r.URL.Query().Get("mid") != "122541" || len(r.URL.Query()) != 1 {
			t.Fatalf("unexpected creator request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(creatorSuccessBody)), Request: r}, nil
	})
	before := time.Now().UTC()
	profile, err := p.GetCreator(context.Background(), "122541")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || profile.Platform != "bilibili" || profile.ID != "122541" || profile.Name != "测试UP" ||
		profile.Description != "个人签名" || profile.AvatarURL != "https://example.com/avatar.jpg" ||
		profile.URL != "https://space.bilibili.com/122541" || profile.FollowerCount == nil || *profile.FollowerCount != 1234 ||
		profile.SourceURL != creatorEndpoint+"?mid=122541" || profile.ObservedAt.Before(before) || profile.ObservedAt.After(time.Now()) {
		t.Fatalf("unexpected creator profile: %+v", profile)
	}
}

func TestInvalidCreatorIDsDoNotMakeRequests(t *testing.T) {
	p := New()
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid UID must not reach upstream")
		return nil, nil
	})
	for _, id := range []string{"", "0", "-1", "+123", "00123", " 123", "123 ", "12.3", "1e3", "１２３", "9223372036854775808", "BV17x411w7KC", "https://space.bilibili.com/123", "123?x=y"} {
		t.Run(id, func(t *testing.T) {
			_, err := p.GetCreator(context.Background(), id)
			assertKind(t, err, source.InvalidInput)
		})
	}
}

func TestCreatorFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		kind   source.ErrorKind
	}{
		{"restricted", 412, "private response", source.AccessRestricted},
		{"rate limited", 429, "", source.RateLimited},
		{"missing endpoint", 404, "", source.UpstreamFailure},
		{"not found", 200, `{"code":-404}`, source.NotFound},
		{"blocked", 200, `{"code":-352}`, source.AccessRestricted},
		{"HTML", 200, "<html>blocked</html>", source.InvalidResponse},
		{"missing code", 200, `{"data":{}}`, source.InvalidResponse},
		{"missing data", 200, `{"code":0}`, source.InvalidResponse},
		{"null data", 200, `{"code":0,"data":null}`, source.InvalidResponse},
		{"missing card", 200, `{"code":0,"data":{}}`, source.InvalidResponse},
		{"null card", 200, `{"code":0,"data":{"card":null}}`, source.InvalidResponse},
		{"missing UID", 200, strings.Replace(creatorSuccessBody, `"mid":"122541",`, "", 1), source.InvalidResponse},
		{"wrong UID", 200, strings.Replace(creatorSuccessBody, "122541", "123456", 1), source.InvalidResponse},
		{"fractional UID", 200, strings.Replace(creatorSuccessBody, `"mid":"122541"`, `"mid":122541.5`, 1), source.InvalidResponse},
		{"missing name", 200, strings.Replace(creatorSuccessBody, `"name":"测试UP",`, "", 1), source.InvalidResponse},
		{"negative followers", 200, strings.Replace(creatorSuccessBody, `"follower":1234`, `"follower":-1`, 1), source.InvalidResponse},
		{"wrong follower type", 200, strings.Replace(creatorSuccessBody, `"follower":1234`, `"follower":"1234"`, 1), source.InvalidResponse},
		{"oversize", 200, strings.Repeat(" ", maxResponseSize+1), source.InvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := withResponse(tc.status, tc.body).GetCreator(context.Background(), "122541")
			assertKind(t, err, tc.kind)
		})
	}
}

func TestCreatorOptionalFieldsAndUIDPrecision(t *testing.T) {
	for _, tc := range []struct {
		name, id, body string
		wantFollowers  *int64
	}{
		{"unknown followers", "122541", `{"code":0,"data":{"card":{"mid":"122541","name":"UP"}}}`, nil},
		{"null followers", "122541", `{"code":0,"data":{"card":{"mid":"122541","name":"UP"},"follower":null}}`, nil},
		{"zero followers", "122541", `{"code":0,"data":{"card":{"mid":122541,"name":"UP"},"follower":0}}`, new(int64)},
		{"large numeric UID", "9007199254740993", `{"code":0,"data":{"card":{"mid":9007199254740993,"name":"UP"}}}`, nil},
		{"large string UID", "9223372036854775807", `{"code":0,"data":{"card":{"mid":"9223372036854775807","name":"UP"}}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := withResponse(200, tc.body).GetCreator(context.Background(), tc.id)
			if err != nil || profile.ID != tc.id {
				t.Fatalf("creator result: %+v, %v", profile, err)
			}
			if (profile.FollowerCount == nil) != (tc.wantFollowers == nil) ||
				(tc.wantFollowers != nil && *profile.FollowerCount != *tc.wantFollowers) {
				t.Fatalf("follower count: %v", profile.FollowerCount)
			}
		})
	}
}

func TestCreatorContextAndReadErrors(t *testing.T) {
	p := New()
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.GetCreator(ctx, "122541")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	_, err = p.GetCreator(context.Background(), "122541")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: failingBody{}, Request: r}, nil
	})
	_, err = p.GetCreator(context.Background(), "122541")
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read failure lost: %v", err)
	}
}
