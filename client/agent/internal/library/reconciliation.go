// reconciliation.go 核对当前服务器任务，仅在仍本地关注且明确无任务时补申请。
// 核对/提交共用目标锁，写回验证代际和关注身份；业务失败有界退避。见 docs/public-response.md。
package library

import (
	"bili-history-tracker/client/agent/internal/remote"
	"context"
	"errors"
	"time"
)

// Reconcile在浏览、后台和手动重试间合并同目标操作；force只提前本地调度，不绕过暂停。
func (s *Service) Reconcile(ctx context.Context, kind, id string, force bool) (Follow, error) {
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	f, base, err := s.find(kind, id)
	if err != nil {
		return f, err
	}
	key := kind + ":" + id
	s.mu.Lock()
	if s.inFlight[key] {
		s.mu.Unlock()
		if !force {
			return f, nil
		}
		return f, &Error{Message: "目标正在核对，请稍后刷新。", Status: 409}
	}
	if !force && (f.NeedsAttention || f.NextAttempt != nil && f.NextAttempt.After(time.Now()) || f.CheckedAt != nil && time.Since(*f.CheckedAt) < time.Minute) {
		s.mu.Unlock()
		return f, nil
	}
	s.inFlight[key] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.inFlight, key); s.mu.Unlock() }()
	status, err := s.client.Status(ctx, base, kind, id)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return f, ctx.Err()
		}
		return s.checkFailed(f, base, err)
	}
	if status.Tracking || status.TrackingState == "paused" || status.State == "paused" {
		return s.confirm(f, base, status)
	}
	// 查询结果只对原关注和原服务器有效；发出申请前再次验证，避免慢查询跨取关/切服。
	s.mu.Lock()
	valid := base == s.state.ServerURL && f.epoch == s.serverEpoch
	found := false
	for _, current := range s.state.Follows {
		if current.Kind == kind && current.ID == id && sameFollow(current, f) {
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !valid || !found {
		return f, &Error{Message: "服务器或本地关注已改变，请刷新。", Status: 409}
	}
	// 保存已经核实的“无任务”，但不清零之前的失败次数，避免每次查询成功让重试无上限。
	_, err = s.update(f, base, func(v *Follow) {
		now := time.Now().UTC()
		v.Status = &status
		v.ServerURL = base
		v.CheckedAt = &now
		v.Pending = true
	})
	if err != nil {
		return f, err
	}
	status, err = s.client.Submit(ctx, base, kind, id)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return f, ctx.Err()
		}
		var failure *remote.Error
		if errors.As(err, &failure) && failure.Code == "TARGET_PAUSED" && failure.Target != nil {
			return s.confirm(f, base, *failure.Target)
		}
		return s.checkFailed(f, base, err)
	}
	return s.confirm(f, base, status)
}

func (s *Service) confirm(f Follow, base string, status remote.Status) (Follow, error) {
	return s.update(f, base, func(v *Follow) {
		now := time.Now().UTC()
		v.Status = &status
		v.ServerURL = base
		v.CheckedAt = &now
		v.Pending = !status.Tracking && status.TrackingState != "paused" && status.State != "paused"
		v.LastError = ""
		v.LastCode = ""
		v.LastStage, v.LastOutcome = "", ""
		v.Attempts = 0
		v.NextAttempt = nil
		v.NeedsAttention = false
		if status.TrackingState == "paused" || status.State == "paused" {
			v.LastCode = "TARGET_PAUSED"
			v.LastError = "目标已被服务器暂停，不会自动恢复。"
		}
	})
}

func (s *Service) checkFailed(f Follow, base string, cause error) (Follow, error) {
	var failure *remote.Error
	code := "REMOTE_ERROR"
	retry := time.Duration(0)
	if errors.As(cause, &failure) {
		code = failure.Code
		retry = failure.RetryAfter
	}
	updated, err := s.update(f, base, func(v *Follow) {
		v.LastError = cause.Error()
		v.LastCode = code
		v.LastStage, v.LastOutcome = "", ""
		if failure != nil {
			v.LastStage, v.LastOutcome = failure.Stage, failure.Outcome
		}
		v.Pending = true
		v.Attempts++
		v.NeedsAttention = v.Attempts >= 6
		delay := min(time.Duration(1<<min(v.Attempts-1, 4))*time.Minute, 15*time.Minute)
		if retry > delay {
			delay = retry
		}
		next := time.Now().UTC().Add(delay)
		v.NextAttempt = &next
	})
	if err != nil {
		return f, err
	}
	return updated, cause
}

// BrowseCheck仅处理仍被本地关注的目标；公共详情浏览不创建私人关注或公共任务。
func (s *Service) BrowseCheck(ctx context.Context, kind, id string) {
	_, _ = s.Reconcile(ctx, kind, id, false)
}
