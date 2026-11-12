// Package remote 通过固定8182路径读取公共资料和申请跟踪，不转发本地 Cookie 或会话。
// 限制响应大小、超时和重定向，不提供任意 URL 代理。见 docs/client-library.md。
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
	"time"
)

type Error struct {
	Message string
	Status  int
}

func (e *Error) Error() string { return e.Message }

type Item struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerID     string `json:"owner_id"`
	OwnerName   string `json:"owner_name"`
	Metric      *int64 `json:"metric"`
	Growth      *int64 `json:"growth"`
	GrowthFrom  int64  `json:"growth_from"`
	GrowthTo    int64  `json:"growth_to"`
	Seq         int64  `json:"seq"`
	ObservedAt  int64  `json:"observed_at"`
	PublishedAt int64  `json:"published_at"`
	Recorded    bool   `json:"recorded"`
	Tracking    bool   `json:"tracking"`
	Status      string `json:"status"`
}
type Status struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Recorded bool   `json:"recorded"`
	Tracking bool   `json:"tracking"`
	State    string `json:"status"`
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
	Item    Item `json:"item"`
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
type Client struct{ http *http.Client }

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
		return &Error{Message: "无法连接服务器，请检查地址、网络和服务是否运行。", Status: 502}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return &Error{Message: "服务器响应不完整或超出大小限制。", Status: 502}
	}
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if media != "application/json" {
		return &Error{Message: "服务器未返回客户端 JSON 数据，请更新远程 backend 后重试。", Status: 502}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		message := "服务器未完成操作。"
		if failure.Error != "" && len(failure.Error) < 500 {
			message = failure.Error
		}
		status := 502
		if response.StatusCode == 404 || response.StatusCode == 409 || response.StatusCode == 429 {
			status = response.StatusCode
		}
		return &Error{Message: message, Status: status}
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
	return result, err
}
func (c *Client) Detail(ctx context.Context, base, kind, id string, page int, seq int64) (Detail, error) {
	var result Detail
	q := url.Values{"days": {"30"}, "page": {strconv.Itoa(page)}}
	if seq > 0 {
		q.Set("seq", strconv.FormatInt(seq, 10))
	}
	err := c.call(ctx, base, "GET", "/detail/"+url.PathEscape(kind)+"/"+url.PathEscape(id)+"?"+q.Encode(), nil, &result)
	if err == nil && (result.Item.Kind != kind || result.Item.ID != id) {
		err = &Error{Message: "服务器资料标识不匹配。", Status: 502}
	}
	return result, err
}
func (c *Client) Submit(ctx context.Context, base, kind, id string) (Status, error) {
	var result Status
	err := c.call(ctx, base, "POST", "/targets", map[string]string{"type": kind, "id": id}, &result)
	if err == nil && (result.Kind != kind || result.ID != id || !result.Tracking) {
		err = &Error{Message: "服务器没有确认目标已加入跟踪。", Status: 502}
	}
	return result, err
}
