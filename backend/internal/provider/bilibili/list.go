// list.go 通过公开合集接口发现作者名下可跟踪的视频。
// B站空间投稿列表接口需要登录态和风控参数，未登录时通常返回 412 或 -799；
// 因此这里只使用无需登录的合集接口，并把空结果视为“没有可发现的合集”，
// 调用方仍需支持手动添加 BV 作为补充。

package bilibili

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const seriesEndpoint = "https://api.bilibili.com/x/polymer/web-space/seasons_series_list"

var _ source.VideoLister = (*Provider)(nil)

// GetCreatorVideos 返回作者合集内去重后的视频，按合集返回顺序排列。
// 该结果不代表作者全部投稿；没有合集或接口拒绝访问时返回错误或空列表。
func (p *Provider) GetCreatorVideos(ctx context.Context, id string) ([]source.CreatorVideo, error) {
	uid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != id {
		return nil, &source.Error{Kind: source.InvalidInput, Platform: platform}
	}
	endpoint := seriesEndpoint + "?" + url.Values{
		"mid":       {id},
		"page_num":  {"1"},
		"page_size": {"20"},
	}.Encode()
	var data struct {
		ItemsLists struct {
			SeasonsList []struct {
				Archives []struct {
					BVID    string `json:"bvid"`
					Title   string `json:"title"`
					Pubdate int64  `json:"pubdate"`
				} `json:"archives"`
			} `json:"seasons_list"`
		} `json:"items_lists"`
	}
	if _, err := p.get(ctx, endpoint, &data); err != nil {
		return nil, err
	}
	videos := make([]source.CreatorVideo, 0)
	seen := make(map[string]struct{})
	for _, season := range data.ItemsLists.SeasonsList {
		for _, archive := range season.Archives {
			if archive.BVID == "" {
				continue
			}
			if _, exists := seen[archive.BVID]; exists {
				continue
			}
			seen[archive.BVID] = struct{}{}
			var publishedAt *time.Time
			if archive.Pubdate > 0 {
				published := time.Unix(archive.Pubdate, 0).UTC()
				publishedAt = &published
			}
			videos = append(videos, source.CreatorVideo{
				ID:          archive.BVID,
				Title:       archive.Title,
				PublishedAt: publishedAt,
			})
		}
	}
	return videos, nil
}
