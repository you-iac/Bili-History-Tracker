// ingest.go 复用单视频查询与保存链路，先验证作者归属，再关注所选视频。
// 批量协调由 discovery 用例负责；不在作者资料采集后自动发现视频。
// 设计说明：docs/video-discovery.md。
package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

// GetCreatorVideoPage 透传平台注册的可选分页能力，不解释平台游标。
func (t *Tracker) GetCreatorVideoPage(ctx context.Context, platform, id, cursor string) (source.CreatorVideoPage, error) {
	if platform != t.platform {
		return source.CreatorVideoPage{}, &source.Error{Kind: source.UnsupportedPlatform, Platform: platform}
	}
	pager, ok := t.queries.(interface {
		GetCreatorVideoPage(context.Context, string, string, string) (source.CreatorVideoPage, error)
	})
	if !ok {
		return source.CreatorVideoPage{}, &source.Error{Kind: source.UnsupportedOperation, Platform: platform}
	}
	return pager.GetCreatorVideoPage(ctx, platform, id, cursor)
}

// IngestVideo 校验公开详情的作者后复用持久化。返回 added/existing/rejected；
// 同一目标正在采集时返回可稍后显式重试的错误，不并发写入同一内容。
func (t *Tracker) IngestVideo(ctx context.Context, creator, id string, interval time.Duration) (string, error) {
	key := "video:" + id
	t.mu.Lock()
	if t.running[key] {
		t.mu.Unlock()
		return "failed", errors.New("target busy")
	}
	t.running[key] = true
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.running, key); t.mu.Unlock() }()
	states, err := t.store.VideoStates(ctx, t.platform, []string{id})
	if err != nil {
		return "failed", err
	}
	if states[id].Subscribed {
		return "existing", nil
	}
	video, err := t.queries.GetVideo(ctx, t.platform, id)
	if err != nil {
		return "failed", err
	}
	if video.Platform != t.platform || video.ID != id || video.Creator.ID != creator {
		return "rejected", nil
	}
	target := storage.Target{Platform: t.platform, TargetType: "video", TargetID: id, IntervalSeconds: int64(interval.Seconds())}
	_, err = t.persistVideo(ctx, target, video)
	if err != nil {
		return "failed", err
	}
	if err = t.store.AddTarget(ctx, t.platform, "video", id, interval, time.Now()); err != nil {
		return "failed", err
	}
	if err = t.store.MarkTargetRun(ctx, target, time.Now(), time.Now().Add(interval)); err != nil {
		return "failed", err
	}
	t.recordOutcome(target, "ok")
	if states[id].Recorded {
		return "existing", nil
	}
	return "added", nil
}

// persistVideo 供定时采集与显式导入共用指纹、版本、指标和成功日志处理。
func (t *Tracker) persistVideo(ctx context.Context, target storage.Target, video source.Video) (storage.SaveResult, error) {
	fingerprint, err := storage.VideoFingerprint(video.Raw)
	if err != nil {
		return storage.SaveResult{}, err
	}
	result, err := t.store.SaveVideo(ctx, video, fingerprint)
	if err != nil {
		return result, err
	}
	t.log.Add(Entry{Time: time.Now(), Level: "info", Target: targetLabel(target), Action: "query_video", Message: "ok", Detail: fmt.Sprintf("seq=%d name=%s", result.Seq, video.Title), VersionAdded: result.VersionAdded})
	if video.ViewCount != nil {
		t.recordMetric(ctx, "video", video.ID, "view", *video.ViewCount)
	}
	return result, nil
}
