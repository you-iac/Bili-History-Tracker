// list_page.go 在适配器内部遍历公开合集目录和成员，向通用层只返回视频页。
// 一次调用最多读取一个上游页；不携带 Cookie、不调用空间投稿接口。
// 内部游标包含遍历位置及去重集合，有容量限制；设计见 docs/video-discovery.md。
package bilibili

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/url"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const seasonMembersEndpoint = "https://api.bilibili.com/x/polymer/web-space/seasons_archives_list"
const seriesMembersEndpoint = "https://api.bilibili.com/x/series/archives"
const discoveryPageSize = 30
const maxDiscoveryIDs = 20000

var _ source.CreatorVideoPager = (*Provider)(nil)

type listCollection struct {
	Kind string
	ID   int64
	Page int
	Last string
}
type listCursor struct {
	Creator       string
	Directory     int
	DirectoryDone bool
	LastDirectory string
	Queue         []listCollection
	Collections   map[string]bool
	Videos        map[string]bool
}
type listArchive struct {
	BVID     string `json:"bvid"`
	Title    string `json:"title"`
	Pic      string `json:"pic"`
	Pubdate  int64  `json:"pubdate"`
	Duration *int64 `json:"duration"`
	Stat     *struct {
		View *int64 `json:"view"`
	} `json:"stat"`
}

// GetCreatorVideoPage 返回扁平视频页。NextCursor 非空时即使 Items 为空也应续页。
// 错误不推进游标，调用者可在冷却后显式恢复；成功页不会因为错误而丢失。
func (p *Provider) GetCreatorVideoPage(ctx context.Context, id, cursor string) (source.CreatorVideoPage, error) {
	uid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != id {
		return source.CreatorVideoPage{}, &source.Error{Kind: source.InvalidInput, Platform: platform}
	}
	state := listCursor{Creator: id, Directory: 1, Collections: map[string]bool{}, Videos: map[string]bool{}}
	if cursor != "" {
		if len(cursor) > 2<<20 {
			return source.CreatorVideoPage{}, &source.Error{Kind: source.InvalidInput, Platform: platform}
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &state) != nil || state.Creator != id || state.Directory < 1 || state.Collections == nil || state.Videos == nil {
			return source.CreatorVideoPage{}, &source.Error{Kind: source.InvalidInput, Platform: platform}
		}
	}
	if len(state.Collections) > maxDiscoveryIDs || len(state.Videos) > maxDiscoveryIDs {
		return source.CreatorVideoPage{}, &source.Error{Kind: source.InvalidResponse, Platform: platform}
	}
	result := source.CreatorVideoPage{Items: []source.CreatorVideo{}}
	if len(state.Queue) == 0 && !state.DirectoryDone {
		var data struct {
			ItemsLists *struct {
				Page struct {
					Total int `json:"total"`
				} `json:"page"`
				Seasons []struct {
					Meta struct {
						ID int64 `json:"season_id"`
					} `json:"meta"`
				} `json:"seasons_list"`
				Series []struct {
					Meta struct {
						ID int64 `json:"series_id"`
					} `json:"meta"`
				} `json:"series_list"`
			} `json:"items_lists"`
		}
		endpoint := seriesEndpoint + "?" + url.Values{"mid": {id}, "page_num": {strconv.Itoa(state.Directory)}, "page_size": {"20"}}.Encode()
		if err := p.listGet(ctx, endpoint, &data); err != nil {
			return result, err
		}
		if data.ItemsLists == nil {
			return result, &source.Error{Kind: source.InvalidResponse, Platform: platform}
		}
		before := len(state.Collections)
		add := func(kind string, id int64) {
			if id <= 0 {
				return
			}
			key := fmt.Sprintf("%s:%d", kind, id)
			if !state.Collections[key] {
				state.Collections[key] = true
				state.Queue = append(state.Queue, listCollection{Kind: kind, ID: id, Page: 1})
			}
		}
		for _, c := range data.ItemsLists.Seasons {
			add("season", c.Meta.ID)
		}
		for _, c := range data.ItemsLists.Series {
			add("series", c.Meta.ID)
		}
		count := len(data.ItemsLists.Seasons) + len(data.ItemsLists.Series)
		if count > 0 && len(state.Collections) == before {
			return result, &source.Error{Kind: source.InvalidResponse, Platform: platform}
		}
		// 以空目录页确认枚举结束，避免不同版本 total（条目数或页数）口径导致提前截断。
		state.DirectoryDone = count == 0
		state.Directory++
	} else if len(state.Queue) > 0 {
		c := &state.Queue[0]
		if c.Page < 1 || c.ID <= 0 || (c.Kind != "season" && c.Kind != "series") {
			return result, &source.Error{Kind: source.InvalidInput, Platform: platform}
		}
		params := url.Values{"mid": {id}}
		endpoint := seasonMembersEndpoint
		if c.Kind == "season" {
			params.Set("season_id", strconv.FormatInt(c.ID, 10))
			params.Set("page_num", strconv.Itoa(c.Page))
			params.Set("page_size", strconv.Itoa(discoveryPageSize))
		} else {
			endpoint = seriesMembersEndpoint
			params.Set("series_id", strconv.FormatInt(c.ID, 10))
			params.Set("pn", strconv.Itoa(c.Page))
			params.Set("ps", strconv.Itoa(discoveryPageSize))
			params.Set("only_normal", "true")
			params.Set("sort", "desc")
		}
		var data struct {
			Archives *[]listArchive `json:"archives"`
			Page     struct {
				Total int `json:"total"`
			} `json:"page"`
		}
		if err := p.listGet(ctx, endpoint+"?"+params.Encode(), &data); err != nil {
			return result, err
		}
		if data.Archives == nil {
			return result, &source.Error{Kind: source.InvalidResponse, Platform: platform}
		}
		archives := *data.Archives
		encoded, _ := json.Marshal(archives)
		sum := sha256.Sum256(encoded)
		signature := fmt.Sprintf("%x", sum)
		if len(archives) > 0 && c.Last == signature {
			return result, &source.Error{Kind: source.InvalidResponse, Platform: platform}
		}
		for _, a := range archives {
			if !bvidPattern.MatchString(a.BVID) {
				return result, &source.Error{Kind: source.InvalidResponse, Platform: platform}
			}
			if state.Videos[a.BVID] {
				continue
			}
			state.Videos[a.BVID] = true
			video := toCreatorVideo(a.BVID, a.Title, a.Pubdate)
			video.CoverURL = a.Pic
			video.DurationSeconds = a.Duration
			if a.Stat != nil {
				video.ViewCount = a.Stat.View
			}
			result.Items = append(result.Items, video)
		}
		if len(archives) < discoveryPageSize || (data.Page.Total > 0 && c.Page*discoveryPageSize >= data.Page.Total) {
			state.Queue = state.Queue[1:]
		} else {
			c.Page++
			c.Last = signature
		}
	}
	if len(state.Collections) > maxDiscoveryIDs || len(state.Videos) > maxDiscoveryIDs {
		return source.CreatorVideoPage{}, &source.Error{Kind: source.InvalidResponse, Platform: platform}
	}
	if len(state.Queue) > 0 || !state.DirectoryDone {
		raw, _ := json.Marshal(state)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}

func waitList(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Provider) listGet(ctx context.Context, endpoint string, data any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	wait := p.listWait
	if wait == nil {
		wait = waitList
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.listMu.Lock()
		cooldown := time.Until(p.listCooldown)
		p.listMu.Unlock()
		delay := time.Duration(0)
		if attempt > 0 {
			delay = time.Duration(1<<(attempt-1))*250*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond
		}
		if cooldown > delay {
			delay = cooldown
		}
		if delay > 0 {
			if err := wait(ctx, delay); err != nil {
				return err
			}
		}
		_, last = p.getRequest(ctx, endpoint, data, false)
		if last == nil {
			return nil
		}
		var se *source.Error
		if errors.As(last, &se) && se.Kind == source.RateLimited {
			if se.RetryAt.Before(time.Now()) {
				se.RetryAt = time.Now().Add(time.Second)
			}
			p.listMu.Lock()
			if se.RetryAt.After(p.listCooldown) {
				p.listCooldown = se.RetryAt
			}
			p.listMu.Unlock()
		}
		log.Printf("bilibili video discovery attempt=%d error=%v", attempt+1, last)
		if ctx.Err() != nil || !retryList(last) {
			return last
		}
	}
	return last
}

func retryList(err error) bool {
	var se *source.Error
	if errors.As(err, &se) {
		return se.Kind == source.RateLimited || (se.Kind == source.UpstreamFailure && (se.HTTPStatus == 500 || se.HTTPStatus == 502 || se.HTTPStatus == 503 || se.HTTPStatus == 504))
	}
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}
