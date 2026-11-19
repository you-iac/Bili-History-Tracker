// metadata.go 查询单个目标的公开当前资料，不使用扫描 Cookie，不枚举投稿。
// 缺失计数保留未知，失败不判定删除。见 docs/client-detail-data-sources.md。
package bilibili

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// Metadata 不含服务器 seq 或跟踪状态，不能作为服务器历史观测写回。
type Metadata struct {
	Kind, ID, Name, Description, Cover, OwnerID, OwnerName string
	Metric                                                 *int64
	ObservedAt, PublishedAt                                int64
}

// Current 只接受规范 UID/BV，响应必须与请求身份一致；上下文控制单次调用。
func (c *Client) Current(ctx context.Context, kind, id string) (Metadata, error) {
	r := Metadata{Kind: kind, ID: id}
	// 即使调用方注入了登录态Jar，公开资料也不继承其Cookie；Transport仍可复用。
	client := *c.http
	client.Jar = nil
	public := Client{http: &client}
	invalid := &Error{Code: "invalid_response", Message: "平台返回的当前资料无效或身份不匹配。"}
	switch kind {
	case "creator":
		uid, err := CreatorID(id)
		if err != nil || uid != id {
			return r, &Error{Code: "invalid_uid", Message: "UID 无效。"}
		}
		var data struct {
			Card struct {
				ID   json.Number `json:"mid"`
				Name string      `json:"name"`
				Sign string      `json:"sign"`
				Face string      `json:"face"`
			} `json:"card"`
			Follower *int64 `json:"follower"`
		}
		if err = public.get(ctx, "/x/web-interface/card", url.Values{"mid": {id}}, "", id, &data); err != nil {
			return r, err
		}
		if data.Card.ID.String() != id || data.Card.Name == "" {
			return r, invalid
		}
		r.Name = data.Card.Name
		r.Description = data.Card.Sign
		r.Cover = data.Card.Face
		r.Metric = data.Follower
	case "video":
		if !bvidPattern.MatchString(id) {
			return r, &Error{Code: "invalid_bvid", Message: "BV 号无效。"}
		}
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
			Stat struct {
				Views *int64 `json:"view"`
			} `json:"stat"`
		}
		if err := public.get(ctx, "/x/web-interface/view", url.Values{"bvid": {id}}, "", "", &data); err != nil {
			return r, err
		}
		owner, err := strconv.ParseInt(data.Owner.ID.String(), 10, 64)
		if data.ID != id || data.Title == "" || err != nil || owner < 1 || strconv.FormatInt(owner, 10) != data.Owner.ID.String() || data.Published < 0 || data.Published > 253402300799 {
			return r, invalid
		}
		r.Name = data.Title
		r.Description = data.Description
		r.Cover = data.Cover
		r.OwnerID = data.Owner.ID.String()
		r.OwnerName = data.Owner.Name
		r.PublishedAt = data.Published * 1000
		r.Metric = data.Stat.Views
	default:
		return r, &Error{Code: "invalid_input", Message: "资料类型无效。"}
	}
	if r.Metric != nil && *r.Metric < 0 {
		return r, invalid
	}
	r.ObservedAt = time.Now().UTC().UnixMilli()
	return r, nil
}
