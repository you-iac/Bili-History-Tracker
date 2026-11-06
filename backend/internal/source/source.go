// Package source 定义平台无关的查询接口、数据结构和错误，并按平台注册表分发查询。
// 本包判断数据源是否支持所需能力，具体平台的标识校验和网络请求由 Provider 实现。
//
// 输入：调用方（tracker、httpapi）传入的平台名、目标 ID 和 context。
// 输出：Video、CreatorProfile、CreatorVideo 或 *Error，原路返回给调用方；
// 本包不落库、不编码 HTTP 响应。
// 它不认识：SQLite、HTTP 路由、调度间隔、B站端点及其字段格式。
//
// 上下游关系：
//
//	┌────────────────────────────┐                                  ┌────────────────────────────┐
//	│ tracker                    │                                  │ httpapi                    │
//	│ 查询 / 合集发现            │                                  │ 视频 / 作者查询            │
//	└────────────────────────────┘                                  └────────────────────────────┘
//	               │                                                               │
//	 platform / ID │                                                 platform / ID │
//	               └───────────────────────────────┬───────────────────────────────┘
//	                                               ▼
//	                                ┌────────────────────────────┐
//	                                │ Service                    │
//	                                │ 平台注册表 / lookup        │
//	                                └────────────────────────────┘
//	                                               │
//	                                               │ 按平台选择
//	                                               │
//	                                               ▼
//	                                ┌────────────────────────────┐
//	                                │ 能力接口                   │
//	                                │ Provider + 可选能力        │
//	                                └────────────────────────────┘
//	                                               │
//	                                               │ 调用具体实现
//	                                               │
//	                                               ▼
//	                                ┌────────────────────────────┐
//	                                │ provider/bilibili          │
//	                                │ 统一资料 / 列表 / 错误     │
//	                                └────────────────────────────┘
//
// 设计说明（仓库相对路径）：docs/architecture.md#module-navigation。
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Creator 是视频结果中的简要作者引用，独立资料查询使用 CreatorProfile。
type Creator struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreatorProfile is an independently observed creator profile. Creator remains
// the compact author reference embedded in Video, so video responses stay stable.
// A nil FollowerCount means the source did not provide a count.
// Raw keeps the unmodified upstream business object for persistence; it is never
// serialized into API responses.
type CreatorProfile struct {
	Platform      string    `json:"platform"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	AvatarURL     string    `json:"avatar_url"`
	URL           string    `json:"url"`
	FollowerCount *int64    `json:"follower_count"`
	ObservedAt    time.Time `json:"observed_at"`
	SourceURL     string    `json:"source_url"`
	Raw           json.RawMessage `json:"-"`
}

// Video is observed metadata, not proof that a video can currently be played.
// A nil PublishedAt means the source did not supply a publication time.
// A nil ViewCount means the source did not supply a count. Raw keeps the
// unmodified upstream business object for persistence and is not serialized.
type Video struct {
	Platform        string     `json:"platform"`
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	URL             string     `json:"url"`
	CoverURL        string     `json:"cover_url"`
	Creator         Creator    `json:"creator"`
	PublishedAt     *time.Time `json:"published_at"`
	DurationSeconds int64      `json:"duration_seconds"`
	ViewCount       *int64     `json:"view_count"`
	ObservedAt      time.Time  `json:"observed_at"`
	SourceURL       string     `json:"source_url"`
	Raw             json.RawMessage `json:"-"`
}

// CreatorVideo is a compact entry from a creator's published video list.
type CreatorVideo struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	PublishedAt *time.Time `json:"published_at"`
}

// Provider implementations own platform-specific identifiers, requests and parsing.
// Implementations must be safe for concurrent queries and respect ctx cancellation.
type Provider interface {
	Platform() string
	GetVideo(ctx context.Context, id string) (Video, error)
}

// CreatorProvider is an optional capability of a registered Provider. Platforms
// that only support video queries need not implement profile queries.
type CreatorProvider interface {
	GetCreator(ctx context.Context, id string) (CreatorProfile, error)
}

// VideoLister is an optional capability for discovering a creator's videos.
// Sources without list access can still be tracked by adding video IDs directly.
type VideoLister interface {
	GetCreatorVideos(ctx context.Context, id string) ([]CreatorVideo, error)
}

type ErrorKind string

const (
	InvalidInput         ErrorKind = "invalid_input"
	UnsupportedPlatform  ErrorKind = "unsupported_platform"
	UnsupportedOperation ErrorKind = "unsupported_operation"
	NotFound             ErrorKind = "not_found"
	AccessRestricted     ErrorKind = "access_restricted"
	RateLimited          ErrorKind = "rate_limited"
	UpstreamFailure      ErrorKind = "upstream_failure"
	InvalidResponse      ErrorKind = "invalid_response"
)

// Error preserves diagnostic codes without exposing upstream response bodies.
// NotFound describes a query result; it does not establish deletion or its cause.
type Error struct {
	Kind         ErrorKind
	Platform     string
	HTTPStatus   int
	UpstreamCode int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s query: %s (HTTP %d, upstream code %d)",
		e.Platform, e.Kind, e.HTTPStatus, e.UpstreamCode)
}

// Service dispatches queries to providers. Its registry is immutable after construction.
type Service struct {
	providers map[string]Provider
}

// NewService 创建查询服务并拒绝空平台名或重复注册；构造后不再修改注册表。
func NewService(providers ...Provider) (*Service, error) {
	s := &Service{providers: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		if p == nil || p.Platform() == "" {
			return nil, fmt.Errorf("provider must declare a platform")
		}
		name := p.Platform()
		if _, exists := s.providers[name]; exists {
			return nil, fmt.Errorf("duplicate provider: %s", name)
		}
		s.providers[name] = p
	}
	return s, nil
}

// GetVideo 选择平台并透传视频 ID 与上下文，ID 的格式由具体 Provider 校验。
func (s *Service) GetVideo(ctx context.Context, platform, id string) (Video, error) {
	p, err := s.lookup(platform, id)
	if err != nil {
		return Video{}, err
	}
	return p.GetVideo(ctx, id)
}

// GetCreator 通过可选能力查询作者资料；已注册但不支持该能力的平台返回 UnsupportedOperation。
func (s *Service) GetCreator(ctx context.Context, platform, id string) (CreatorProfile, error) {
	p, err := s.lookup(platform, id)
	if err != nil {
		return CreatorProfile{}, err
	}
	creatorProvider, ok := p.(CreatorProvider)
	if !ok {
		return CreatorProfile{}, &Error{Kind: UnsupportedOperation, Platform: platform}
	}
	return creatorProvider.GetCreator(ctx, id)
}

// ListCreatorVideos 通过可选能力列出作者投稿；平台不支持时返回 UnsupportedOperation。
func (s *Service) ListCreatorVideos(ctx context.Context, platform, id string) ([]CreatorVideo, error) {
	p, err := s.lookup(platform, id)
	if err != nil {
		return nil, err
	}
	lister, ok := p.(VideoLister)
	if !ok {
		return nil, &Error{Kind: UnsupportedOperation, Platform: platform}
	}
	return lister.GetCreatorVideos(ctx, id)
}

func (s *Service) lookup(platform, id string) (Provider, error) {
	if platform == "" || id == "" {
		return nil, &Error{Kind: InvalidInput, Platform: platform}
	}
	p, ok := s.providers[platform]
	if !ok {
		return nil, &Error{Kind: UnsupportedPlatform, Platform: platform}
	}
	return p, nil
}
