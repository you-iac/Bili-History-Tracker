// interactions.go 管理互动自动关注：后台按设置频率同步历史、重查点赞/投币/收藏，
// 并把有互动且未关注的视频自动加入本地关注和服务器跟踪。
// 只依赖 scanner 的只读历史接口，复用 Follow/Submit 的推送链路。见 PLANs/2026-11-24-interaction-follow.md。
package library

import (
	"context"
	"errors"
	"slices"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/scanner"
)

// InteractionSource 是 scanner 提供的互动历史能力；library 只读不写历史。
type InteractionSource interface {
	HistorySync(context.Context) (scanner.HistorySyncResult, error)
	CheckInteractions(context.Context, int) (scanner.InteractionResult, error)
	HistoryVideosFiltered(int, int, int, func(string) bool) scanner.HistoryVideoPage
}

// InteractionConfig 控制后台互动发现；Sources 只允许 like/coin/favorite。
type InteractionConfig struct {
	Enabled         bool     `json:"enabled"`
	Sources         []string `json:"sources"`
	IntervalMinutes int      `json:"interval_minutes"`
	MaxAutoFollow   int      `json:"max_auto_follow"`
}

func defaultInteractionConfig() InteractionConfig {
	return InteractionConfig{Sources: []string{"like", "coin", "favorite"}, IntervalMinutes: 60, MaxAutoFollow: 20}
}

func normalizeInteractionConfig(cfg InteractionConfig) (InteractionConfig, error) {
	allowed := map[string]bool{"like": true, "coin": true, "favorite": true}
	sources := make([]string, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		if !allowed[source] {
			return InteractionConfig{}, errors.New("互动来源无效，只支持 like、coin、favorite。")
		}
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}
	if len(sources) == 0 {
		sources = defaultInteractionConfig().Sources
	}
	if cfg.IntervalMinutes != 30 && cfg.IntervalMinutes != 60 && cfg.IntervalMinutes != 360 {
		return InteractionConfig{}, errors.New("互动检查频率只支持 30、60、360 分钟。")
	}
	if cfg.MaxAutoFollow <= 0 || cfg.MaxAutoFollow > 50 {
		return InteractionConfig{}, errors.New("每轮自动关注数量需在 1-50 之间。")
	}
	cfg.Sources = sources
	return cfg, nil
}

// InteractionSettings 返回当前配置；未保存时返回默认值。
func (s *Service) InteractionSettings() InteractionConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Interactions == nil {
		return defaultInteractionConfig()
	}
	cfg := *s.state.Interactions
	if len(cfg.Sources) == 0 {
		cfg.Sources = defaultInteractionConfig().Sources
	}
	return cfg
}

// SetInteractionSettings 保存配置并唤醒后台循环；无效来源/频率返回 400。
func (s *Service) SetInteractionSettings(cfg InteractionConfig) (InteractionConfig, error) {
	cfg, err := normalizeInteractionConfig(cfg)
	if err != nil {
		return InteractionConfig{}, &Error{Message: err.Error(), Status: 400}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState()
	next.Interactions = &cfg
	if err := s.save(next); err != nil {
		return InteractionConfig{}, err
	}
	select {
	case s.interactionWake <- struct{}{}:
	default:
	}
	return cfg, nil
}

// RunInteractions 与 Agent 生命周期绑定；按配置频率执行历史同步、互动重查和自动关注。
func (s *Service) RunInteractions(ctx context.Context) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.interactionWake:
		case <-timer.C:
		}
		s.runInteractionsOnce(ctx)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(s.interactionInterval())
	}
}

// RunInteractionsOnce 手动执行一轮；不检查 Enabled，供设置页立即触发。
func (s *Service) RunInteractionsOnce(ctx context.Context) {
	s.runInteractionsOnce(ctx)
}

func (s *Service) interactionInterval() time.Duration {
	cfg := s.InteractionSettings()
	if cfg.IntervalMinutes <= 0 {
		return time.Hour
	}
	return time.Duration(cfg.IntervalMinutes) * time.Minute
}

func (s *Service) runInteractionsOnce(ctx context.Context) {
	cfg := s.InteractionSettings()
	if !cfg.Enabled || s.interactions == nil || ctx.Err() != nil {
		return
	}
	if _, err := s.interactions.HistorySync(ctx); err != nil {
		return
	}
	if _, err := s.interactions.CheckInteractions(ctx, 60); err != nil {
		return
	}
	s.autoFollowInteractions(ctx, cfg)
}

// autoFollowInteractions 只关注最近 30 天、有指定互动且尚未关注的视频，单轮有上限。
func (s *Service) autoFollowInteractions(ctx context.Context, cfg InteractionConfig) {
	page := s.interactions.HistoryVideosFiltered(30, 1, 60, nil)
	count := 0
	for _, item := range page.Items {
		if count >= cfg.MaxAutoFollow || ctx.Err() != nil {
			break
		}
		if !interactionHit(item.HistoryItem, cfg.Sources) || s.isFollowed("video", item.BVID) {
			continue
		}
		if _, err := s.Follow(ctx, "video", item.BVID); err != nil {
			continue
		}
		count++
	}
}

func interactionHit(item bilibili.HistoryItem, sources []string) bool {
	for _, source := range sources {
		switch source {
		case "like":
			if item.Like {
				return true
			}
		case "coin":
			if item.Coin > 0 {
				return true
			}
		case "favorite":
			if item.Favorite {
				return true
			}
		}
	}
	return false
}

func (s *Service) isFollowed(kind, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, follow := range s.state.Follows {
		if follow.Kind == kind && follow.ID == id {
			return true
		}
	}
	return false
}
