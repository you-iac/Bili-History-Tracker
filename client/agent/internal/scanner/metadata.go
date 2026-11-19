// metadata.go 为关注库提供本地当前资料读取，不暴露Cookie给库或浏览器。
// 与扫描共享已配置的适配器，网络期间不持锁。见 docs/local-first-metadata.md。
package scanner

import (
	"bili-history-tracker/client/agent/internal/bilibili"
	"context"
	"errors"
)

// Current 在锁外使用当前本地Cookie查询明确目标，关闭后拒绝新请求。
func (s *Service) Current(ctx context.Context, kind, id string) (bilibili.Metadata, error) {
	s.mu.Lock()
	cookie, closed, api := s.cookie, s.closed, s.api
	s.mu.Unlock()
	if closed {
		return bilibili.Metadata{}, errors.New("本地平台服务已关闭。")
	}
	provider, ok := api.(interface {
		Current(context.Context, string, string, string) (bilibili.Metadata, error)
	})
	if !ok {
		return bilibili.Metadata{}, errors.New("本地平台暂不支持当前资料查询。")
	}
	return provider.Current(ctx, cookie, kind, id)
}
