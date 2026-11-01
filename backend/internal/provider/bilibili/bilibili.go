// Package bilibili implements metadata queries against Bilibili's web endpoint.
// This endpoint is not a guaranteed public developer API and may reject requests.
package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const (
	platform        = "bilibili"
	videoEndpoint   = "https://api.bilibili.com/x/web-interface/view"
	maxResponseSize = 2 << 20 // 2 MiB; metadata only.
)

var bvidPattern = regexp.MustCompile(`^BV[1-9A-HJ-NP-Za-km-z]{10}$`)

var _ source.Provider = (*Provider)(nil)

type Provider struct {
	client *http.Client
}

// New uses a dedicated HTTP client. The adapter never supplies login cookies,
// retries requests, or follows redirects to alternative data sources.
func New() *Provider {
	return &Provider{client: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (*Provider) Platform() string { return platform }

type videoData struct {
	BVID     string `json:"bvid"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Pic      string `json:"pic"`
	Pubdate  int64  `json:"pubdate"`
	Duration int64  `json:"duration"`
	Owner    struct {
		MID  int64  `json:"mid"`
		Name string `json:"name"`
	} `json:"owner"`
}

func (p *Provider) GetVideo(ctx context.Context, id string) (source.Video, error) {
	if !bvidPattern.MatchString(id) {
		return source.Video{}, &source.Error{Kind: source.InvalidInput, Platform: platform}
	}
	endpoint := videoEndpoint + "?" + url.Values{"bvid": {id}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return source.Video{}, fmt.Errorf("build bilibili request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bili-History-Tracker/0.1")
	resp, err := p.client.Do(req)
	if err != nil {
		// Preserve cancellation and timeout errors for the caller.
		return source.Video{}, fmt.Errorf("query bilibili: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		kind := source.UpstreamFailure
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusPreconditionFailed:
			kind = source.AccessRestricted
		case http.StatusTooManyRequests:
			kind = source.RateLimited
		}
		// A missing API route (HTTP 404) does not mean the video is missing.
		return source.Video{}, &source.Error{Kind: kind, Platform: platform, HTTPStatus: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return source.Video{}, fmt.Errorf("read bilibili response: %w", err)
	}
	invalidResponse := &source.Error{Kind: source.InvalidResponse, Platform: platform}
	if len(body) > maxResponseSize {
		return source.Video{}, invalidResponse
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil {
		return source.Video{}, invalidResponse
	}
	if *envelope.Code != 0 {
		return source.Video{}, apiError(*envelope.Code)
	}
	var data videoData
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) ||
		json.Unmarshal(envelope.Data, &data) != nil {
		return source.Video{}, invalidResponse
	}
	if data.BVID != id || data.Title == "" || data.Owner.MID <= 0 || data.Duration < 0 || data.Pubdate < 0 {
		return source.Video{}, invalidResponse
	}
	var publishedAt *time.Time
	if data.Pubdate > 0 {
		t := time.Unix(data.Pubdate, 0).UTC()
		publishedAt = &t
	}
	return source.Video{
		Platform:        platform,
		ID:              data.BVID,
		Title:           data.Title,
		Description:     data.Desc,
		URL:             "https://www.bilibili.com/video/" + data.BVID,
		CoverURL:        data.Pic,
		Creator:         source.Creator{ID: strconv.FormatInt(data.Owner.MID, 10), Name: data.Owner.Name},
		PublishedAt:     publishedAt,
		DurationSeconds: data.Duration,
		ObservedAt:      time.Now().UTC(),
		SourceURL:       endpoint,
	}, nil
}

func apiError(code int) error {
	kind := source.UpstreamFailure
	switch code {
	case -400:
		kind = source.InvalidInput
	case -404:
		kind = source.NotFound
	case -101, -102, -403, -352, 62002, 62004, 62012:
		kind = source.AccessRestricted
	case -509, -799:
		kind = source.RateLimited
	}
	return &source.Error{Kind: kind, Platform: platform, UpstreamCode: code}
}
