// summaries.go 从服务器已记录目录同步本地关注摘要，读取不依赖关注提交是否成功。
// 摘要和默认详情缓存分开保存；不把分页/时间范围不同的资料冒充完整详情。见 docs/client-catalog.md。
package library

import (
	"context"
	"strconv"
	"strings"
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

// SyncSummaries按当前页ID精确同步；显式ID来自浏览页时先核对，旧调用默认最多24项。
// 最多20秒；相同服务器代际/目标集合的并发同步合并，不扫描公共全库。
// 中途失败仍保存摘要和旧缓存；浏览核对可申请跟踪，实际采集仍由服务器管理。
func (s *Service) SyncSummaries(ctx context.Context, kind string, ids ...string) (int, error) {
	if kind != "creator" && kind != "video" {
		return 0, &Error{Message: "资料类型无效。", Status: 400}
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	browse := len(ids) > 0
	requested := map[string]bool{}
	if len(ids) == 0 {
		for _, f := range s.state.Follows {
			if f.Kind == kind {
				ids = append(ids, f.ID)
				if len(ids) >= 24 {
					break
				}
			}
		}
	}
	if len(ids) > 24 {
		s.mu.Unlock()
		return 0, &Error{Message: "每轮最多同步24项。", Status: 400}
	}
	for _, id := range ids {
		requested[id] = true
	}
	key := base + "|" + strconv.FormatUint(epoch, 10) + "|" + kind + "|" + strings.Join(ids, ",")
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
		if f.Kind == kind && requested[f.ID] {
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
	for _, id := range ids {
		if _, ok := targets[id]; !ok {
			continue
		}
		if browse {
			s.mu.Lock()
			valid := base == s.state.ServerURL && epoch == s.serverEpoch
			found := false
			for _, current := range s.state.Follows {
				if current.Kind == kind && current.ID == id && sameFollow(current, targets[id]) {
					found = true
					break
				}
			}
			s.mu.Unlock()
			if !valid {
				readErr = &Error{Message: "服务器已切换，请刷新。", Status: 409}
				break
			}
			if !found {
				continue
			}
			s.BrowseCheck(ctx, kind, id)
		}
		data, err := s.client.Catalog(ctx, base, kind, "", id, 1)
		if err != nil {
			readErr = err
			break
		}
		if data.Page != 1 {
			readErr = &Error{Message: "服务器目录页码不匹配。", Status: 502}
			break
		}
		for _, item := range data.Items {
			if item.ID == id && item.Kind == kind {
				items[id] = item
			}
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
			if tracked && found && f.Kind == kind && sameFollow(f, original) && setSummary(&next.Follows[index], item, base, now) {
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
