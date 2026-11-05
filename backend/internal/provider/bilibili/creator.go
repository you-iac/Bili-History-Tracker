// creator.go 按 UID 查询 UP 主资料，校验响应中的身份并归一化资料字段。
// 请求处理复用 http.go；粉丝数缺失时保持未知，不触发投稿列表查询。
// 设计说明（仓库相对路径）：docs/architecture.md 的“按 UID 查询 UP 主资料”章节。

package bilibili

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const creatorEndpoint = "https://api.bilibili.com/x/web-interface/card"

var _ source.CreatorProvider = (*Provider)(nil)

// GetCreator 接受规范的正十进制 UID，返回本次观测资料；无效 UID 不发起上游请求。
func (p *Provider) GetCreator(ctx context.Context, id string) (source.CreatorProfile, error) {
	// UID is a canonical positive decimal integer. Do not accept URLs, signs,
	// leading zeroes or values that cannot be represented by the source's int64.
	uid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != id {
		return source.CreatorProfile{}, &source.Error{Kind: source.InvalidInput, Platform: platform}
	}
	endpoint := creatorEndpoint + "?" + url.Values{"mid": {id}}.Encode()
	var data struct {
		Card *struct {
			MID  json.Number `json:"mid"`
			Name string      `json:"name"`
			Face string      `json:"face"`
			Sign string      `json:"sign"`
		} `json:"card"`
		Follower *int64 `json:"follower"`
	}
	raw, err := p.get(ctx, endpoint, &data)
	if err != nil {
		return source.CreatorProfile{}, err
	}
	invalidResponse := &source.Error{Kind: source.InvalidResponse, Platform: platform}
	if data.Card == nil {
		return source.CreatorProfile{}, invalidResponse
	}
	// Bilibili returns card.mid as a string. json.Number also handles numeric IDs
	// without converting through floating point and losing large UID precision.
	observedUID, err := data.Card.MID.Int64()
	if err != nil || observedUID != uid || data.Card.Name == "" ||
		(data.Follower != nil && *data.Follower < 0) {
		return source.CreatorProfile{}, invalidResponse
	}
	return source.CreatorProfile{
		Platform:      platform,
		ID:            id,
		Name:          data.Card.Name,
		Description:   data.Card.Sign,
		AvatarURL:     data.Card.Face,
		URL:           "https://space.bilibili.com/" + id,
		FollowerCount: data.Follower,
		ObservedAt:    time.Now().UTC(),
		SourceURL:     endpoint,
		Raw:           raw,
	}, nil
}
