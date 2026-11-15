// pending.go 只重试尚未确认的公共跟踪申请，不采集本地周期指标。
// 每轮最多3项，失败指数退避至15分钟；申请幂等且可取消。见 docs/client-workspace.md。
package library

import (
	"context"
	"time"
)

// RunPending 与Agent主生命周期绑定，启动及连接更改后处理待提交项；无需网页保持打开。
func (s *Service) RunPending(ctx context.Context) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}
		s.mu.Lock()
		pending := []Follow{}
		if s.state.ServerURL != "" {
			for _, f := range s.state.Follows {
				if f.Pending && !s.inFlight[f.Kind+":"+f.ID] && (f.NextAttempt == nil || !f.NextAttempt.After(time.Now())) {
					pending = append(pending, f)
					if len(pending) == 3 {
						break
					}
				}
			}
		}
		s.mu.Unlock()
		for _, f := range pending {
			if ctx.Err() != nil {
				return
			}
			attempt, cancel := context.WithTimeout(ctx, 22*time.Second)
			_, err := s.Submit(attempt, f.Kind, f.ID)
			cancel()
			if err == nil {
				refresh, cancel := context.WithTimeout(ctx, 20*time.Second)
				_, _ = s.Refresh(refresh, f.Kind, f.ID)
				cancel()
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(30 * time.Second)
	}
}
