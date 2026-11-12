// request.go 接纳 Agent 显式提交的目标：先查询验证并保存，再建立公共跟踪。
// 同目标并发返回忙，已跟踪目标直接复用，不刷新或改变间隔。见 docs/client-library.md。
package tracker

import (
	"context"
	"errors"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

var ErrTargetBusy = errors.New("target busy")
var ErrTargetPaused = errors.New("target paused")
var ErrRequestCapacity = errors.New("target request capacity")

// RequestTarget 不接受客户端元数据或调度设置。kind/id 应由入口校验；失败不创建跟踪目标。
// 并发上限覆盖本进程正在执行的采集，防止显式申请无限制启动上游请求。
func (t *Tracker) RequestTarget(ctx context.Context, kind, id string) (string, error) {
	if kind != "video" && kind != "creator" {
		return "", errors.New("invalid target kind")
	}
	key := kind + ":" + id
	t.mu.Lock()
	if t.running[key] {
		t.mu.Unlock()
		return "", ErrTargetBusy
	}
	if len(t.running) >= 16 {
		t.mu.Unlock()
		return "", ErrRequestCapacity
	}
	t.running[key] = true
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.running, key); t.mu.Unlock() }()
	old, exists, err := t.store.TrackingTarget(ctx, t.platform, kind, id)
	if err != nil {
		return "", err
	}
	if exists {
		if !old.Enabled {
			return "", ErrTargetPaused
		}
		return "existing", nil
	}
	target := storage.Target{Platform: t.platform, TargetType: kind, TargetID: id, IntervalSeconds: int64(DefaultInterval.Seconds())}
	if kind == "video" {
		video, err := t.queries.GetVideo(ctx, t.platform, id)
		if err != nil {
			return "", err
		}
		if video.Platform != t.platform || video.ID != id || video.Creator.ID == "" {
			return "", errors.New("invalid upstream video")
		}
		if _, err = t.persistVideo(ctx, target, video); err != nil {
			return "", err
		}
	} else {
		profile, err := t.queries.GetCreator(ctx, t.platform, id)
		if err != nil {
			return "", err
		}
		if profile.Platform != t.platform || profile.ID != id {
			return "", errors.New("invalid upstream creator")
		}
		fingerprint, err := storage.CreatorFingerprint(profile.Raw)
		if err != nil {
			return "", err
		}
		if _, err = t.store.SaveCreator(ctx, profile, fingerprint); err != nil {
			return "", err
		}
		if profile.FollowerCount != nil {
			t.recordMetric(ctx, "creator", id, "follower", *profile.FollowerCount)
		}
	}
	inserted, err := t.store.AddRequestedTarget(ctx, target, time.Now())
	if err != nil {
		return "", err
	}
	if !inserted {
		current, _, err := t.store.TrackingTarget(ctx, t.platform, kind, id)
		if err != nil {
			return "", err
		}
		if !current.Enabled {
			return "", ErrTargetPaused
		}
		return "existing", nil
	}
	t.recordOutcome(target, "ok")
	return "tracking", nil
}
