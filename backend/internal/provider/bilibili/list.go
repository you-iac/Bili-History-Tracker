// list.go 发现作者名下可跟踪的视频。
//
// 有 BHT_BILI_COOKIE 时优先调用空间投稿列表接口，按投稿时间分页，最多取 maxListedVideos 个；
// 该接口未登录时通常返回 412 或 -799，失败后回退到公开合集接口。没有 Cookie 时只使用合集接口，
// 因此发现结果可能不完整，调用方应支持手动添加 BV 作为补充。

package bilibili

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const (
	seriesEndpoint  = "https://api.bilibili.com/x/polymer/web-space/seasons_series_list"
	spaceEndpoint   = "https://api.bilibili.com/x/space/arc/search"
	maxListedVideos = 120
	spacePageSize   = 30
)

var _ source.VideoLister = (*Provider)(nil)

// GetCreatorVideos 返回作者投稿列表；带 Cookie 时优先空间接口，失败后回退合集。
func (p *Provider) GetCreatorVideos(ctx context.Context, id string) ([]source.CreatorVideo, error) {
	uid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != id {
		return nil, &source.Error{Kind: source.InvalidInput, Platform: platform}
	}
	if p.cookie != "" {
		if videos, err := p.listSpaceVideos(ctx, id); err == nil {
			return videos, nil
		}
	}
	return p.listSeasonVideos(ctx, id)
}

// listSpaceVideos 按投稿时间分页调用空间列表接口，需要登录态才可能成功。
func (p *Provider) listSpaceVideos(ctx context.Context, id string) ([]source.CreatorVideo, error) {
	videos := make([]source.CreatorVideo, 0, spacePageSize)
	seen := make(map[string]struct{})
	for page := 1; page <= (maxListedVideos+spacePageSize-1)/spacePageSize; page++ {
		endpoint := spaceEndpoint + "?" + url.Values{
			"mid":      {id},
			"ps":       {strconv.Itoa(spacePageSize)},
			"pn":       {strconv.Itoa(page)},
			"order":    {"pubdate"},
			"platform": {"web"},
		}.Encode()
		var data struct {
			List struct {
				VList []struct {
					BVID    string `json:"bvid"`
					Title   string `json:"title"`
					Pubdate int64  `json:"pubdate"`
				} `json:"vlist"`
			} `json:"list"`
			Page struct {
				Count int `json:"count"`
			} `json:"page"`
		}
		if _, err := p.get(ctx, endpoint, &data); err != nil {
			return nil, err
		}
		for _, item := range data.List.VList {
			if item.BVID == "" {
				continue
			}
			if _, exists := seen[item.BVID]; exists {
				continue
			}
			seen[item.BVID] = struct{}{}
			videos = append(videos, toCreatorVideo(item.BVID, item.Title, item.Pubdate))
			if len(videos) >= maxListedVideos {
				return videos, nil
			}
		}
		if len(data.List.VList) < spacePageSize {
			break
		}
		if data.Page.Count > 0 && page*spacePageSize >= data.Page.Count {
			break
		}
	}
	return videos, nil
}

// listSeasonVideos 读取公开合集内的视频，不需要登录态但可能不完整。
func (p *Provider) listSeasonVideos(ctx context.Context, id string) ([]source.CreatorVideo, error) {
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
			videos = append(videos, toCreatorVideo(archive.BVID, archive.Title, archive.Pubdate))
		}
	}
	return videos, nil
}

func toCreatorVideo(bvid, title string, pubdate int64) source.CreatorVideo {
	var publishedAt *time.Time
	if pubdate > 0 {
		published := time.Unix(pubdate, 0).UTC()
		publishedAt = &published
	}
	return source.CreatorVideo{ID: bvid, Title: title, PublishedAt: publishedAt}
}
