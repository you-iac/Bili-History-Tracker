// interactions.go 查询单个视频的点赞/投币状态，并汇总收藏夹内的视频集合。
// 点赞/投币没有完整列表接口，只能对已知历史视频逐条查询；收藏夹可一次分页拉取。
package bilibili

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// Relation 是当前用户对单个视频的互动关系；一次请求可同时得到点赞、投币和收藏。
type Relation struct {
	Like      bool
	Coin      int
	Favorite  bool
	Attention bool
}

// ArchiveRelation 查询当前用户对视频的点赞/投币/收藏状态；需要登录态 Cookie。
func (c *Client) ArchiveRelation(ctx context.Context, cookie, bvid string, keys Keys) (Relation, error) {
	if !bvidPattern.MatchString(bvid) {
		return Relation{}, &Error{Code: "invalid_bvid", Message: "视频 ID 无效。"}
	}
	query := keys.Sign(url.Values{"bvid": {bvid}}, time.Now())
	var data struct {
		Like      bool `json:"like"`
		Coin      int  `json:"coin"`
		Favorite  bool `json:"favorite"`
		Attention bool `json:"attention"`
	}
	if err := c.get(ctx, "/x/web-interface/archive/relation", query, cookie, "", &data); err != nil {
		return Relation{}, err
	}
	if data.Coin < 0 {
		return Relation{}, &Error{Code: "invalid_response", Message: "B站返回的互动数据无效。"}
	}
	return Relation{Like: data.Like, Coin: data.Coin, Favorite: data.Favorite, Attention: data.Attention}, nil
}

// HasLike 查询当前用户是否点赞该视频；需要登录态 Cookie。
func (c *Client) HasLike(ctx context.Context, cookie, bvid string, keys Keys) (bool, error) {
	if !bvidPattern.MatchString(bvid) {
		return false, &Error{Code: "invalid_bvid", Message: "视频 ID 无效。"}
	}
	query := keys.Sign(url.Values{"bvid": {bvid}}, time.Now())
	var data int
	if err := c.get(ctx, "/x/web-interface/archive/has/like", query, cookie, "", &data); err != nil {
		return false, err
	}
	return data == 1, nil
}

// Coins 返回当前用户对该视频的投币数（0-2）；需要登录态 Cookie。
func (c *Client) Coins(ctx context.Context, cookie, bvid string, keys Keys) (int, error) {
	if !bvidPattern.MatchString(bvid) {
		return 0, &Error{Code: "invalid_bvid", Message: "视频 ID 无效。"}
	}
	query := keys.Sign(url.Values{"bvid": {bvid}}, time.Now())
	var data struct {
		Multiply int `json:"multiply"`
	}
	if err := c.get(ctx, "/x/web-interface/archive/coins", query, cookie, "", &data); err != nil {
		return 0, err
	}
	if data.Multiply < 0 {
		return 0, &Error{Code: "invalid_response", Message: "B站返回的投币数据无效。"}
	}
	return data.Multiply, nil
}

// FavoriteSet 返回当前账号全部收藏夹内的 bvid 集合；分页拉取，最多每夹 50 页。
func (c *Client) FavoriteSet(ctx context.Context, cookie string, mid int64, keys Keys) (map[string]bool, error) {
	if mid <= 0 {
		return nil, &Error{Code: "invalid_uid", Message: "账号 ID 无效。"}
	}
	query := keys.Sign(url.Values{"up_mid": {strconv.FormatInt(mid, 10)}}, time.Now())
	var folders struct {
		List []struct {
			ID         int64 `json:"id"`
			MediaCount int   `json:"media_count"`
		} `json:"list"`
	}
	if err := c.get(ctx, "/x/v3/fav/folder/created/list-all", query, cookie, "", &folders); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, folder := range folders.List {
		if folder.MediaCount <= 0 {
			continue
		}
		for page := 1; page <= 50; page++ {
			params := keys.Sign(url.Values{
				"media_id": {strconv.FormatInt(folder.ID, 10)},
				"pn":       {strconv.Itoa(page)},
				"ps":       {"20"},
			}, time.Now())
			var data struct {
				Medias []struct {
					BVID string `json:"bvid"`
				} `json:"medias"`
				HasMore bool `json:"has_more"`
			}
			if err := c.get(ctx, "/x/v3/fav/resource/list", params, cookie, "", &data); err != nil {
				return nil, err
			}
			for _, media := range data.Medias {
				if bvidPattern.MatchString(media.BVID) {
					set[media.BVID] = true
				}
			}
			if !data.HasMore || len(data.Medias) == 0 {
				break
			}
		}
	}
	return set, nil
}
