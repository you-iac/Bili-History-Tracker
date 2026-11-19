// metadata.go 在本机读取视频/UP当前公开资料，不获取媒体或写服务器历史。
// Cookie只进入固定B站端点；身份与空指标边界见 docs/local-first-metadata.md。
package bilibili

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// Metadata 是本地当前资料，不携带Cookie、原始响应或服务器版本号。
type Metadata struct {
	Kind, ID, Name, Cover, OwnerID, OwnerName string
	Metric                                    *int64
	PublishedAt, At                           int64
}

// Current 读取一个明确目标；不枚举投稿，不把未知数量当成零。
func (c *Client) Current(ctx context.Context, cookie, kind, id string) (Metadata, error) {
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
	item := Metadata{Kind: kind, ID: id, At: time.Now().UTC().UnixMilli()}
	bad := &Error{Code: "invalid_response", Message: "B站返回的资料身份或字段不完整。"}
	if kind == "creator" {
		var data struct {
			Card *struct {
				MID  json.Number `json:"mid"`
				Name string      `json:"name"`
				Face string      `json:"face"`
			} `json:"card"`
			Follower *int64 `json:"follower"`
		}
		if err := c.get(ctx, "/x/web-interface/card", url.Values{"mid": {id}}, cookie, id, &data); err != nil {
			return Metadata{}, err
		}
		if data.Card == nil || data.Card.MID.String() != id || data.Card.Name == "" || data.Follower != nil && *data.Follower < 0 {
			return Metadata{}, bad
		}
		item.Name, item.Cover, item.Metric = data.Card.Name, data.Card.Face, data.Follower
	} else {
		var data struct {
			ID        string `json:"bvid"`
			Title     string `json:"title"`
			Pic       string `json:"pic"`
			Published int64  `json:"pubdate"`
			Owner     struct {
				MID  int64  `json:"mid"`
				Name string `json:"name"`
			} `json:"owner"`
			Stat *struct {
				View *int64 `json:"view"`
			} `json:"stat"`
		}
		if err := c.get(ctx, "/x/web-interface/view", url.Values{"bvid": {id}}, cookie, "", &data); err != nil {
			return Metadata{}, err
		}
		if data.ID != id || data.Title == "" || data.Owner.MID <= 0 || data.Published < 0 || data.Published > 9223372036854 {
			return Metadata{}, bad
		}
		item.Name, item.Cover, item.OwnerID, item.OwnerName, item.PublishedAt = data.Title, data.Pic, strconv.FormatInt(data.Owner.MID, 10), data.Owner.Name, data.Published*1000
		if data.Stat != nil {
			item.Metric = data.Stat.View
			if item.Metric != nil && *item.Metric < 0 {
				return Metadata{}, bad
			}
		}
	}
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
