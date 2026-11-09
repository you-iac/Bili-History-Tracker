// http.go 为视频、UP 主和合集查询提供共用的 HTTP 请求、业务码检查及 JSON 数据解析。
// get 同时返回解析后的业务结构和原始 data；各查询适配器决定存档范围，本层不裁剪。
// 设计说明（仓库相对路径）：docs/architecture.md 的“服务端在哪里请求 B站”章节。

package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/source"
)

// get applies the same transport and envelope checks to every query.
// endpoint is constructed inside this package, never supplied by an API caller.
// It returns the raw data object; query adapters prepare snapshots before persistence.
func (p *Provider) get(ctx context.Context, endpoint string, data any) (json.RawMessage, error) {
	return p.getRequest(ctx, endpoint, data, true)
}

// getRequest 的 public 调用明确不携带进程配置的 Cookie。
func (p *Provider) getRequest(ctx context.Context, endpoint string, data any, credentials bool) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build bilibili request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bili-History-Tracker/0.1")
	if credentials && p.cookie != "" {
		req.Header.Set("Cookie", p.cookie)
	}
	req.Header.Set("Referer", "https://space.bilibili.com/")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query bilibili: %w", err)
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
		// A missing API route does not mean the requested resource is missing.
		var retryAt time.Time
		if value := resp.Header.Get("Retry-After"); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 && seconds <= 86400 {
				retryAt = time.Now().Add(time.Duration(seconds) * time.Second)
			} else if at, err := http.ParseTime(value); err == nil {
				retryAt = at
			}
		}
		return nil, &source.Error{Kind: kind, Platform: platform, HTTPStatus: resp.StatusCode, RetryAt: retryAt}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read bilibili response: %w", err)
	}
	invalidResponse := &source.Error{Kind: source.InvalidResponse, Platform: platform}
	if len(body) > maxResponseSize {
		return nil, invalidResponse
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil {
		return nil, invalidResponse
	}
	if *envelope.Code != 0 {
		return nil, apiError(*envelope.Code)
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) ||
		json.Unmarshal(envelope.Data, data) != nil {
		return nil, invalidResponse
	}
	return envelope.Data, nil
}
