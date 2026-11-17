// Package remote 通过8181公共协议的固定路径读取资料和申请跟踪，不转发本地 Cookie 或会话。
// 限制响应大小、超时和重定向，不提供任意 URL 代理。目录透传白名单排序和 UP 归属。
// 业务JSON、旧HTTP兼容和统一节流见 docs/public-response.md；资料口径见 docs/client-catalog.md。
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Error struct {
	Message    string
	Status     int
	Code       string
	Target     *Status
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

type Item struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	OwnerID      string `json:"owner_id"`
	OwnerName    string `json:"owner_name"`
	Metric       *int64 `json:"metric"`
	Growth       *int64 `json:"growth"`
	VideoViews   *int64 `json:"video_views"`
	RecentVideos *int64 `json:"recent_videos"`
	VideoCount   *int64 `json:"video_count"`
	Cover        string `json:"cover,omitempty"`
	GrowthFrom   int64  `json:"growth_from"`
	GrowthTo     int64  `json:"growth_to"`
	Seq          int64  `json:"seq"`
	ObservedAt   int64  `json:"observed_at"`
	PublishedAt  int64  `json:"published_at"`
	Recorded     bool   `json:"recorded"`
	Tracking     bool   `json:"tracking"`
	Status       string `json:"status"`
}
type Status struct {
	TrackingState string `json:"tracking_state,omitempty"`
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Recorded      bool   `json:"recorded"`
	Tracking      bool   `json:"tracking"`
	State         string `json:"status"`
}
type Point struct {
	At    int64 `json:"at"`
	Value int64 `json:"value"`
}
type Version struct {
	Seq  int64  `json:"seq"`
	At   int64  `json:"at"`
	Name string `json:"name"`
}
type Detail struct {
	Target  *Status `json:"target,omitempty"`
	Item    Item    `json:"item"`
	Metrics struct {
		Points    []Point `json:"points"`
		Truncated bool    `json:"truncated"`
	} `json:"metrics"`
	Versions     []Version       `json:"versions"`
	VersionPage  int             `json:"version_page"`
	VersionTotal int             `json:"version_total"`
	VersionLimit int             `json:"version_limit"`
	Snapshot     json.RawMessage `json:"snapshot,omitempty"`
	SnapshotSeq  int64           `json:"snapshot_seq,omitempty"`
}
type Client struct {
	http *http.Client
	mu   sync.Mutex
	next time.Time
}

// wait统一控制目录、图片和核对请求的启动速率，限流冷却也作用于并发调用。
func (c *Client) wait(ctx context.Context) error {
	for {
		c.mu.Lock()
		delay := time.Until(c.next)
		if delay <= 0 {
			c.next = time.Now().Add(150 * time.Millisecond)
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type Catalog struct {
	Items []Item `json:"items"`
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
	Total int    `json:"total"`
}

// CatalogOptions 仅选择已有公开记录的顺序和 UP 归属；不触发平台采集。
type CatalogOptions struct {
	Sort, OwnerID string
}

// Overview 由Agent读取既有公开库接口聚合，不依赖新增服务器总览接口。
// CatalogBased 区分旧版根接口缓存，分页长度识别旧五条预览；旧缓存不继续展示。见 docs/client-workspace.md。
type Overview struct {
	Summary struct {
		Creators int64 `json:"creators"`
		Videos   int64 `json:"videos"`
	} `json:"summary"`
	Creators     []Item    `json:"creators"`
	Videos       []Item    `json:"videos"`
	FetchedAt    time.Time `json:"fetched_at"`
	CatalogBased bool      `json:"catalog_based"`
	CreatorLimit int       `json:"creator_limit"`
	VideoLimit   int       `json:"video_limit"`
}

// Catalog 只读服务器已记录的数据；id 是精确标识，q 是名称/标识搜索，不调用平台发现。
func (c *Client) Catalog(ctx context.Context, base, kind, q, id string, page int, options ...CatalogOptions) (Catalog, error) {
	var result Catalog
	path := "/creators"
	if kind == "video" {
		path = "/videos"
	} else if kind != "creator" {
		return result, &Error{Message: "搜索类型无效。", Status: 400}
	}
	query := url.Values{"q": {q}, "page": {strconv.Itoa(page)}}
	if len(options) > 0 {
		query.Set("sort", options[0].Sort)
		if options[0].OwnerID != "" {
			query.Set("owner", options[0].OwnerID)
		}
	}
	if id != "" {
		query.Set("id", id)
		query.Del("q")
	}
	err := c.call(ctx, base, "GET", path+"?"+query.Encode(), nil, &result)
	if err == nil && (result.Items == nil || result.Page < 1 || result.Limit < 1 || result.Total < 0) {
		err = &Error{Message: "服务器公开库格式不兼容，请更新 backend。", Status: 502}
	}
	return result, err
}

// Overview 只读两种公开库的第一页与total，不抓取HTML或推算全局跟踪/版本统计。
func (c *Client) Overview(ctx context.Context, base string) (Overview, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	creators, err := c.Catalog(ctx, base, "creator", "", "", 1)
	if err != nil {
		return Overview{}, err
	}
	videos, err := c.Catalog(ctx, base, "video", "", "", 1)
	if err != nil {
		return Overview{}, err
	}
	result := Overview{Creators: creators.Items, Videos: videos.Items, FetchedAt: time.Now().UTC(), CatalogBased: true, CreatorLimit: creators.Limit, VideoLimit: videos.Limit}
	result.Summary.Creators, result.Summary.Videos = int64(creators.Total), int64(videos.Total)
	return result, nil
}

// New 使用独立客户端，不共享平台 CookieJar；禁止重定向。
func New(transport http.RoundTripper) *Client {
	return &Client{http: &http.Client{Transport: transport, Timeout: 22 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}}
}
func NormalizeBase(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(input, "\r\n") || strings.Contains(u.Path, "..") {
		return "", &Error{Message: "服务器地址需为 HTTP/HTTPS 基地址，不包含账号、查询串或片段。", Status: 400}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}
func (c *Client) call(ctx context.Context, base, method, path string, body any, out any) error {
	if base == "" {
		return &Error{Message: "请先配置服务器地址；本地关注仍可独立使用。", Status: 422}
	}
	var input io.Reader
	if err := c.wait(ctx); err != nil {
		return &Error{Message: "请求等待超时或已取消，请稍后核对。", Status: 502, Code: "REQUEST_TIMEOUT", Cause: err}
	}
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	r, err := http.NewRequestWithContext(ctx, method, base+path, input)
	if err != nil {
		return &Error{Message: "服务器地址无效。", Status: 400}
	}
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return &Error{Message: "请求超时或已取消，请稍后核对服务器状态。", Status: 502, Code: "REQUEST_TIMEOUT", Cause: ctx.Err()}
		}
		return &Error{Message: "无法连接服务器，请检查地址、网络和服务是否运行。", Status: 502, Code: "CONNECTION_FAILED"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return &Error{Message: "服务器响应不完整或超出大小限制。", Status: 502}
	}
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if media != "application/json" {
		return &Error{Message: "服务器未返回客户端 JSON 数据，请确认连接 8181 公共接口端口，并更新远程 backend。", Status: 502}
	}
	var failure struct {
		OK     *bool           `json:"ok"`
		Code   string          `json:"code"`
		Error  json.RawMessage `json:"error"`
		Target *Status         `json:"target"`
		Retry  int             `json:"retry_after_seconds"`
	}
	if err = json.Unmarshal(data, &failure); err != nil {
		return &Error{Message: "服务器数据格式不兼容，请更新backend。", Status: 502, Code: "INVALID_RESPONSE"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || failure.OK != nil && !*failure.OK || failure.Code != "" && failure.Code != "OK" || len(failure.Error) > 0 && string(failure.Error) != "null" {
		message := "服务器未完成操作。"
		var explanation string
		if json.Unmarshal(failure.Error, &explanation) == nil && explanation != "" && len(explanation) < 500 {
			message = explanation
		}
		status := 502
		if response.StatusCode == 404 || response.StatusCode == 409 || response.StatusCode == 429 {
			status = response.StatusCode
		}
		code := failure.Code
		if code == "" {
			switch response.StatusCode {
			case 404:
				code = "DETAIL_NOT_RECORDED"
			case 429:
				code = "RATE_LIMITED"
			case 409:
				code = "TARGET_CONFLICT"
			default:
				code = "REMOTE_ERROR"
			}
		}
		switch code {
		case "DETAIL_NOT_RECORDED", "SNAPSHOT_NOT_FOUND":
			status = 404
		case "TARGET_BUSY", "TARGET_PAUSED":
			status = 409
		case "RATE_LIMITED":
			status = 429
		}
		if failure.Retry <= 0 {
			failure.Retry, _ = strconv.Atoi(response.Header.Get("Retry-After"))
		}
		retry := time.Duration(max(0, min(failure.Retry, 900))) * time.Second
		if code == "RATE_LIMITED" {
			if retry < time.Second {
				retry = time.Second
			}
			c.mu.Lock()
			until := time.Now().Add(retry)
			if until.After(c.next) {
				c.next = until
			}
			c.mu.Unlock()
		}
		return &Error{Message: message, Status: status, Code: code, Target: failure.Target, RetryAfter: retry}
	}
	if err = json.Unmarshal(data, out); err != nil {
		return &Error{Message: "服务器数据格式不兼容，请更新 backend。", Status: 502}
	}
	return nil
}
func (c *Client) Test(ctx context.Context, base string) error {
	var page struct {
		Items json.RawMessage `json:"items"`
	}
	if err := c.call(ctx, base, "GET", "/creators", nil, &page); err != nil {
		return err
	}
	if len(page.Items) == 0 {
		return &Error{Message: "服务器缺少客户端资料接口，请更新 backend。", Status: 502}
	}
	return nil
}
func (c *Client) Status(ctx context.Context, base, kind, id string) (Status, error) {
	var result Status
	err := c.call(ctx, base, "GET", "/api/targets/status?"+url.Values{"kind": {kind}, "id": {id}}.Encode(), nil, &result)
	if err == nil && (result.Kind != kind || result.ID != id) {
		err = &Error{Message: "服务器目标状态不匹配，请更新 backend。", Status: 502}
	}
	if err == nil {
		err = result.normalize(kind, id)
	}
	return result, err
}

func (s *Status) normalize(kind, id string) error {
	if s.Kind != kind || s.ID != id {
		return &Error{Message: "服务器目标状态不匹配。", Status: 502, Code: "INVALID_RESPONSE"}
	}
	if s.TrackingState == "" {
		s.TrackingState = "absent"
		if s.State == "paused" {
			s.TrackingState = "paused"
		} else if s.Tracking {
			s.TrackingState = "active"
		}
	}
	if s.TrackingState != "absent" && s.TrackingState != "active" && s.TrackingState != "paused" || s.Tracking != (s.TrackingState == "active") {
		return &Error{Message: "服务器跟踪状态无效。", Status: 502, Code: "INVALID_RESPONSE"}
	}
	return nil
}

// Detail 的时间范围仅允许服务器已有的7/30/90/all；省略时保留30日行为。
func (c *Client) Detail(ctx context.Context, base, kind, id string, page int, seq int64, ranges ...string) (Detail, error) {
	var result Detail
	days := "30"
	if len(ranges) > 0 && ranges[0] != "" {
		days = ranges[0]
	}
	if days != "7" && days != "30" && days != "90" && days != "all" {
		return result, &Error{Message: "时间范围无效。", Status: 400}
	}
	q := url.Values{"days": {days}, "page": {strconv.Itoa(page)}}
	if seq > 0 {
		q.Set("seq", strconv.FormatInt(seq, 10))
	}
	err := c.call(ctx, base, "GET", "/detail/"+url.PathEscape(kind)+"/"+url.PathEscape(id)+"?"+q.Encode(), nil, &result)
	var failure *Error
	if errors.As(err, &failure) && failure.Target != nil {
		if e := failure.Target.normalize(kind, id); e != nil {
			return result, e
		}
	}
	if err == nil && result.Target != nil {
		if e := result.Target.normalize(kind, id); e != nil {
			return result, e
		}
	}
	if err == nil && (result.Item.Kind != kind || result.Item.ID != id) {
		err = &Error{Message: "服务器资料标识不匹配。", Status: 502}
	}
	return result, err
}
func (c *Client) Submit(ctx context.Context, base, kind, id string) (Status, error) {
	var result Status
	err := c.call(ctx, base, "POST", "/targets", map[string]string{"type": kind, "id": id}, &result)
	var failure *Error
	if errors.As(err, &failure) && failure.Target != nil {
		if e := failure.Target.normalize(kind, id); e != nil {
			return result, e
		}
	}
	if err == nil && (result.Kind != kind || result.ID != id || !result.Tracking) {
		err = &Error{Message: "服务器没有确认目标已加入跟踪。", Status: 502}
	}
	if err == nil {
		err = result.normalize(kind, id)
	}
	return result, err
}
