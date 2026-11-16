// summaries.go 从服务器已记录目录同步本地关注摘要，读取不依赖关注提交是否成功。
// 摘要和默认详情缓存分开保存；不把分页/时间范围不同的资料冒充完整详情。见 docs/client-catalog.md。
package library

import (
	"context"
	"strconv"
	"time"

	"bili-history-tracker/client/agent/internal/remote"
)

type summaryCall struct {
	done  chan struct{}
	count int
	err   error
}

// setSummary 只接纳匹配标识且没有倒退的资料，调用方必须持有 s.mu 并核对服务器代际。
func setSummary(f *Follow, item remote.Item, base string, at time.Time) bool {
	if item.Kind != f.Kind || item.ID != f.ID {
		return false
	}
	current := followItem(*f)
	if f.ServerURL == base && (current.ObservedAt > item.ObservedAt || current.Seq > item.Seq) {
		return false
	}
	f.Summary = &item
	f.SummaryAt = &at
	f.ServerURL = base
	return true
}

// SyncSummaries 分页读取已记录公开目录，仅更新请求开始时仍被本地关注的目标。
// 全部本地目标匹配或服务器目录读完即停止；最多20秒，同源同类型的并发同步合并。
// 中途失败仍保存已经读到的摘要，旧缓存和关注提交状态保留；不会发起上游采集。
func (s *Service) SyncSummaries(ctx context.Context, kind string) (int, error) {
	if kind != "creator" && kind != "video" {
		return 0, &Error{Message: "资料类型无效。", Status: 400}
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	key := base + "|" + strconv.FormatUint(epoch, 10) + "|" + kind
	if call := s.summaryCalls[key]; call != nil {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.count, call.err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	targets := map[string]Follow{}
	for _, f := range s.state.Follows {
		if f.Kind == kind {
			targets[f.ID] = f
		}
	}
	if base == "" || len(targets) == 0 {
		s.mu.Unlock()
		return 0, nil
	}
	call := &summaryCall{done: make(chan struct{})}
	s.summaryCalls[key] = call
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	items := map[string]remote.Item{}
	var readErr error
	for page := 1; page <= 100000; page++ {
		data, err := s.client.Catalog(ctx, base, kind, "", "", page)
		if err != nil {
			readErr = err
			break
		}
		if data.Page != page {
			readErr = &Error{Message: "服务器目录页码不匹配，已保留读取到的资料。", Status: 502}
			break
		}
		for _, item := range data.Items {
			if _, ok := targets[item.ID]; ok && item.Kind == kind {
				items[item.ID] = item
			}
		}
		if len(items) == len(targets) || data.Page*data.Limit >= data.Total {
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		readErr = &Error{Message: "服务器已切换，请重新读取关注资料。", Status: 409}
	} else {
		next := s.copyState()
		now := time.Now().UTC()
		for index, f := range next.Follows {
			original, tracked := targets[f.ID]
			item, found := items[f.ID]
			if tracked && found && f.Kind == kind && f.AddedAt.Equal(original.AddedAt) && setSummary(&next.Follows[index], item, base, now) {
				call.count++
			}
		}
		if call.count > 0 {
			if err := s.save(next); err != nil {
				call.count = 0
				readErr = err
			}
		}
	}
	call.err = readErr
	delete(s.summaryCalls, key)
	close(call.done)
	return call.count, call.err
}
