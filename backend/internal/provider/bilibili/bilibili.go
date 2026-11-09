// Package bilibili 将 B站网页端点的响应转换为 source 中的平台无关数据。
// 本文件负责客户端初始化、BV 查询和业务错误映射；creator.go 负责 UID 资料查询，
// http.go 负责共用的请求与响应校验。网页端点可能拒绝访问，不保证持续可用。
//
// 输入：source 层传来的 BV、UID 和 context，以及 B站 HTTP 响应；
// 输出：Video、CreatorProfile、CreatorVideo 和 *source.Error；其中 Raw 保存
// 适配器准备的业务快照，供 storage 存档与计算指纹。视频经 snapshot.go 仅移除
// 顶层 ugc_season；作者资料保留完整 data。快照规则见 docs/database.md。
// 它不认识：数据库、调度器、8181/8182 的 HTTP 服务；Cookie 只来自 BHT_BILI_COOKIE。
//
// 上下游关系：
//
//	                                ┌────────────────────────────┐
//	                                │ source.Service             │
//	                                │ 视频 / 作者 / 列表请求     │
//	                                └────────────────────────────┘
//	                                               │
//	                                               │
//	               ┌───────────────────────────────┼───────────────────────────────┐
//	               ▼                               ▼                               ▼
//	┌────────────────────────────┐  ┌────────────────────────────┐  ┌────────────────────────────┐
//	│ bilibili.go                │  │ creator.go                 │  │ list.go                    │
//	│ BV 校验 / GetVideo         │  │ UID 校验 / GetCreator      │  │ 投稿 / 公开合集发现        │
//	└────────────────────────────┘  └────────────────────────────┘  └────────────────────────────┘
//	               │                               │                               │
//	               │                               │                               │
//	               └───────────────────────────────┼───────────────────────────────┘
//	                                               ▼
//	                                ┌────────────────────────────┐
//	                                │ http.go                    │
//	                                │ 请求 / 状态与业务码校验    │
//	                                └────────────────────────────┘
//	                                               │
//	                                               │ HTTPS GET
//	                                               │
//	                                               ▼
//	                                ┌────────────────────────────┐
//	                                │ api.bilibili.com           │
//	                                │ 固定端点 / 返回 data       │
//	                                └────────────────────────────┘
//
// 设计说明（仓库相对路径）：docs/architecture.md#module-navigation。
package bilibili

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"bili-history-tracker/backend/internal/source"
)

const (
	platform        = "bilibili"
	videoEndpoint   = "https://api.bilibili.com/x/web-interface/view"
	maxResponseSize = 2 << 20 // 2 MiB; metadata only.
)

var bvidPattern = regexp.MustCompile(`^BV[1-9A-HJ-NP-Za-km-z]{10}$`)

var _ source.Provider = (*Provider)(nil)

// Provider 持有可并发复用的 HTTP 客户端，实现视频查询和可选的作者资料查询能力。
// cookie 来自 BHT_BILI_COOKIE，仅用于访问需要登录态的投稿列表接口。
type Provider struct {
	client       *http.Client
	cookie       string
	listMu       sync.Mutex
	listCooldown time.Time
	listWait     func(context.Context, time.Duration) error
}

// New uses a dedicated HTTP client. The adapter never supplies login cookies
// unless the operator configured BHT_BILI_COOKIE, retries requests, or follows
// redirects to alternative data sources.
func New() *Provider {
	return &Provider{
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cookie: strings.TrimSpace(os.Getenv("BHT_BILI_COOKIE")),
	}
}

func (*Provider) Platform() string { return platform }

type videoData struct {
	BVID     string `json:"bvid"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Pic      string `json:"pic"`
	Pubdate  int64  `json:"pubdate"`
	Duration int64  `json:"duration"`
	Owner    struct {
		MID  int64  `json:"mid"`
		Name string `json:"name"`
	} `json:"owner"`
	Stat *struct {
		View *int64 `json:"view"`
	} `json:"stat"`
}

// GetVideo 校验 BV 并获取视频元数据；查询成功不代表视频当前可以播放。
func (p *Provider) GetVideo(ctx context.Context, id string) (source.Video, error) {
	if !bvidPattern.MatchString(id) {
		return source.Video{}, &source.Error{Kind: source.InvalidInput, Platform: platform}
	}
	endpoint := videoEndpoint + "?" + url.Values{"bvid": {id}}.Encode()
	var data videoData
	raw, err := p.get(ctx, endpoint, &data)
	if err != nil {
		return source.Video{}, err
	}
	invalidResponse := &source.Error{Kind: source.InvalidResponse, Platform: platform}
	if data.BVID != id || data.Title == "" || data.Owner.MID <= 0 || data.Duration < 0 || data.Pubdate < 0 {
		return source.Video{}, invalidResponse
	}
	snapshot, err := videoSnapshot(raw)
	if err != nil {
		return source.Video{}, invalidResponse
	}
	var publishedAt *time.Time
	if data.Pubdate > 0 {
		t := time.Unix(data.Pubdate, 0).UTC()
		publishedAt = &t
	}
	var viewCount *int64
	if data.Stat != nil && data.Stat.View != nil && *data.Stat.View >= 0 {
		viewCount = data.Stat.View
	}
	return source.Video{
		Platform:        platform,
		ID:              data.BVID,
		Title:           data.Title,
		Description:     data.Desc,
		URL:             "https://www.bilibili.com/video/" + data.BVID,
		CoverURL:        data.Pic,
		Creator:         source.Creator{ID: strconv.FormatInt(data.Owner.MID, 10), Name: data.Owner.Name},
		PublishedAt:     publishedAt,
		DurationSeconds: data.Duration,
		ViewCount:       viewCount,
		ObservedAt:      time.Now().UTC(),
		SourceURL:       endpoint,
		Raw:             snapshot,
	}, nil
}

func apiError(code int) error {
	kind := source.UpstreamFailure
	switch code {
	case -400:
		kind = source.InvalidInput
	case -404:
		kind = source.NotFound
	case -101, -102, -403, -352, 62002, 62004, 62012:
		kind = source.AccessRestricted
	case -509, -799:
		kind = source.RateLimited
	}
	return &source.Error{Kind: kind, Platform: platform, UpstreamCode: code}
}
