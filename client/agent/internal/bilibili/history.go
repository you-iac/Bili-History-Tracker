// history.go 读取登录态观看历史，按游标分页；只返回视频与作者字段。
// 接口需要 SESSDATA 与 WBI 签名，不下载媒体、不写服务器。见 docs/client-discovery.md。
package bilibili

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// HistoryPageSize 是历史接口单页上限；B站通常返回不超过该值。
const HistoryPageSize = 30

// HistoryItem 是一次观看记录；同一视频多次观看由调用方按 viewed_at 去重。
// Like/Coin/Favorite 由互动查询补充，InteractionCheckedAt 记录上次查询时间。
type HistoryItem struct {
	BVID                   string `json:"bvid"`
	Title                  string `json:"title"`
	Cover                  string `json:"cover"`
	AuthorID               string `json:"author_id"`
	AuthorName             string `json:"author_name"`
	ViewedAt               int64  `json:"viewed_at"`
	Progress               int64  `json:"progress"`
	Duration               int64  `json:"duration"`
	Like                   bool   `json:"like,omitempty"`
	Coin                   int    `json:"coin,omitempty"`
	Favorite               bool   `json:"favorite,omitempty"`
	InteractionCheckedAt   int64  `json:"interaction_checked_at,omitempty"`
	InteractionFailures    int    `json:"interaction_failures,omitempty"`
	InteractionNextAttempt int64  `json:"interaction_next_attempt,omitempty"`
}

// HistoryPage 是历史游标页；More=false 表示本次同步可以结束。
type HistoryPage struct {
	Items  []HistoryItem
	Max    int64
	ViewAt int64
	More   bool
}

// History 读取一页观看历史。max、viewAt 为 0 表示从最新开始。
func (c *Client) History(ctx context.Context, cookie string, max, viewAt int64, keys Keys) (HistoryPage, error) {
	if max < 0 || viewAt < 0 {
		return HistoryPage{}, &Error{Code: "invalid_cursor", Message: "历史游标无效。"}
	}
	query := url.Values{"business": {"archive"}, "ps": {strconv.Itoa(HistoryPageSize)}}
	if max > 0 {
		query.Set("max", strconv.FormatInt(max, 10))
	}
	if viewAt > 0 {
		query.Set("view_at", strconv.FormatInt(viewAt, 10))
	}
	query = keys.Sign(query, time.Now())
	var data struct {
		Cursor *struct {
			Max    int64 `json:"max"`
			ViewAt int64 `json:"view_at"`
		} `json:"cursor"`
		List []struct {
			Title    string `json:"title"`
			Cover    string `json:"cover"`
			Author   string `json:"author_name"`
			AuthorID int64  `json:"author_mid"`
			ViewAt   int64  `json:"view_at"`
			Progress int64  `json:"progress"`
			Duration int64  `json:"duration"`
			History  struct {
				BVID string `json:"bvid"`
			} `json:"history"`
		} `json:"list"`
	}
	if err := c.get(ctx, "/x/web-interface/history/cursor", query, cookie, "", &data); err != nil {
		return HistoryPage{}, err
	}
	invalid := &Error{Code: "invalid_response", Message: "B站返回的历史数据不完整。"}
	if data.Cursor == nil {
		return HistoryPage{}, invalid
	}
	page := HistoryPage{Max: data.Cursor.Max, ViewAt: data.Cursor.ViewAt}
	for _, item := range data.List {
		if !bvidPattern.MatchString(item.History.BVID) || item.AuthorID <= 0 || item.ViewAt < 0 || item.Progress < 0 || item.Duration < 0 {
			continue
		}
		page.Items = append(page.Items, HistoryItem{
			BVID:       item.History.BVID,
			Title:      item.Title,
			Cover:      item.Cover,
			AuthorID:   strconv.FormatInt(item.AuthorID, 10),
			AuthorName: item.Author,
			ViewedAt:   item.ViewAt,
			Progress:   item.Progress,
			Duration:   item.Duration,
		})
	}
	page.More = len(data.List) >= HistoryPageSize && (data.Cursor.Max > 0 || data.Cursor.ViewAt > 0)
	return page, nil
}
