// metadata.go 在本机读取视频/UP当前公开资料，不获取媒体或写服务器历史。
// Current 不携带登录态，CurrentWithCookie 只把显式传入的 Cookie 发往固定 B站端点。
// 身份、缺失计数与回退边界见 docs/local-first-metadata.md 和 docs/client-detail-data-sources.md。
package bilibili

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// Metadata 是当前资料，不含服务器 seq、跟踪状态或原始响应。
// 缺失计数保留 nil，失败不判定删除；不能作为服务器历史观测写回。
type Metadata struct {
	Kind, ID, Name, Description, Cover, OwnerID, OwnerName string
	Metric                                                 *int64
	ObservedAt, PublishedAt                                int64
}

// Current 读取一个明确目标，不继承登录态，不枚举投稿。
func (c *Client) Current(ctx context.Context, kind, id string) (Metadata, error) {
	return c.current(ctx, "", kind, id)
}

// CurrentWithCookie 使用调用方显式传入的 Cookie；空 Cookie 等价于 Current。
func (c *Client) CurrentWithCookie(ctx context.Context, cookie, kind, id string) (Metadata, error) {
	return c.current(ctx, cookie, kind, id)
}

func (c *Client) current(ctx context.Context, cookie, kind, id string) (Metadata, error) {
	invalid := &Error{Code: "invalid_input", Message: "平台资料目标无效。"}
	if kind == "creator" {
		if normalized, e := CreatorID(id); e != nil || normalized != id {
			return Metadata{}, invalid
		}
	} else if kind != "video" || !bvidPattern.MatchString(id) {
		return Metadata{}, invalid
	}
	select {
	case c.currentSlots <- struct{}{}:
		defer func() { <-c.currentSlots }()
	case <-ctx.Done():
		return Metadata{}, ctx.Err()
	}
	if err := c.waitCurrent(ctx); err != nil {
		return Metadata{}, err
	}
	httpClient := c.http
	if cookie == "" {
		// 公开资料不继承调用方注入的登录态 Jar。
		public := *c.http
		public.Jar = nil
		httpClient = &public
	}
	item := Metadata{Kind: kind, ID: id}
	bad := &Error{Code: "invalid_response", Message: "B站返回的资料身份或字段不完整。"}
	if kind == "creator" {
		var data struct {
			Card *struct {
				MID  json.Number `json:"mid"`
				Name string      `json:"name"`
				Sign string      `json:"sign"`
				Face string      `json:"face"`
			} `json:"card"`
			Follower *int64 `json:"follower"`
		}
		if err := c.getWith(ctx, httpClient, "/x/web-interface/card", url.Values{"mid": {id}}, cookie, id, &data); err != nil {
			return Metadata{}, err
		}
		if data.Card == nil || data.Card.MID.String() != id || data.Card.Name == "" || data.Follower != nil && *data.Follower < 0 {
			return Metadata{}, bad
		}
		item.Name, item.Description, item.Cover, item.Metric = data.Card.Name, data.Card.Sign, data.Card.Face, data.Follower
	} else {
		var data struct {
			ID          string `json:"bvid"`
			Title       string `json:"title"`
			Description string `json:"desc"`
			Cover       string `json:"pic"`
			Published   int64  `json:"pubdate"`
			Owner       struct {
				ID   json.Number `json:"mid"`
				Name string      `json:"name"`
			} `json:"owner"`
			Stat *struct {
				Views *int64 `json:"view"`
			} `json:"stat"`
		}
		if err := c.getWith(ctx, httpClient, "/x/web-interface/view", url.Values{"bvid": {id}}, cookie, "", &data); err != nil {
			return Metadata{}, err
		}
		owner, err := strconv.ParseInt(data.Owner.ID.String(), 10, 64)
		if data.ID != id || data.Title == "" || err != nil || owner < 1 || strconv.FormatInt(owner, 10) != data.Owner.ID.String() || data.Published < 0 || data.Published > 253402300799 {
			return Metadata{}, bad
		}
		item.Name, item.Description, item.Cover = data.Title, data.Description, data.Cover
		item.OwnerID, item.OwnerName, item.PublishedAt = data.Owner.ID.String(), data.Owner.Name, data.Published*1000
		if data.Stat != nil {
			item.Metric = data.Stat.Views
		}
	}
	if item.Metric != nil && *item.Metric < 0 {
		return Metadata{}, bad
	}
	item.ObservedAt = time.Now().UTC().UnixMilli()
	return item, nil
}

func (c *Client) waitCurrent(ctx context.Context) error {
	for {
		c.currentMu.Lock()
		delay := time.Until(c.currentNext)
		if delay <= 0 {
			c.currentNext = time.Now().Add(150 * time.Millisecond)
			c.currentMu.Unlock()
			return nil
		}
		c.currentMu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
