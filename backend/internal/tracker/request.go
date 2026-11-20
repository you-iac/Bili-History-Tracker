// request.go 分阶段接纳显式跟踪申请，查询与写入分别计时；有效资料不受请求断开影响。
// 同目标互斥、重复申请不刷新调度；已提交的部分结果不删除。见 docs/request-stages.md。
package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

var ErrTargetBusy = errors.New("target busy")
var ErrTargetPaused = errors.New("target paused")
var ErrRequestCapacity = errors.New("target request capacity")

// ErrRequestStopping 表示服务已关闭新工作接纳，不能借此推断已有任务或写入结果。
var ErrRequestStopping = errors.New("tracker stopping")

// ErrTargetStorage 标记存储调用错误；错误链仍可能表明根因是取消或超时。
var ErrTargetStorage = errors.New("target storage failure")

// RequestTarget 不接受客户端元数据或调度设置，kind/id 由入口校验。
// 查询遵守请求取消；有效结果进入 persist 后，各写入阶段使用独立预算并由 Stop/Wait 收尾。
// 失败可能发生在部分事务已提交之后；RequestError 标明阶段，调用方必须先核对再重试。
func (t *Tracker) RequestTarget(ctx context.Context, kind, id string) (string, error) {
	if kind != "video" && kind != "creator" {
		return "", errors.New("invalid target kind")
	}
	if err := t.beginTarget(kind, id, true); err != nil {
		return "", &RequestError{Stage: "check", Outcome: "not_applied", Cause: err}
	}
	defer t.endTarget(kind, id)
	queryRoot, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.workCtx, cancel)
	defer stop()
	defer cancel()
	var old storage.Target
	var exists bool
	err := requestStage(queryRoot, t.budgets.Check, "check", "not_applied", func(phase context.Context) error {
		var err error
		old, exists, err = t.requests.TrackingTarget(phase, t.platform, kind, id)
		if err != nil {
			return errors.Join(ErrTargetStorage, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if exists {
		if !old.Enabled {
			return "", ErrTargetPaused
		}
		return "existing", nil
	}
	var video source.Video
	var profile source.CreatorProfile
	var fingerprint storage.Fingerprint
	err = requestStage(queryRoot, t.budgets.Query, "query", "not_applied", func(phase context.Context) error {
		var err error
		if kind == "video" {
			video, err = t.queries.GetVideo(phase, t.platform, id)
			if err != nil {
				return err
			}
			if video.Platform != t.platform || video.ID != id || video.Creator.ID == "" {
				return errors.New("invalid upstream video")
			}
			fingerprint, err = storage.VideoFingerprint(video.Raw)
		} else {
			profile, err = t.queries.GetCreator(phase, t.platform, id)
			if err != nil {
				return err
			}
			if profile.Platform != t.platform || profile.ID != id {
				return errors.New("invalid upstream creator")
			}
			fingerprint, err = storage.CreatorFingerprint(profile.Raw)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	// 无新 goroutine；handler 同步等待。查询即使刚好耗尽预算，只要返回有效资料仍允许保存。
	writeRoot := context.WithoutCancel(ctx)
	err = requestStage(writeRoot, t.budgets.Persist, "persist", "unknown", func(phase context.Context) error {
		var result storage.SaveResult
		var err error
		var metric *int64
		metricName := "view"
		if kind == "video" {
			result, err = t.requests.SaveVideo(phase, video, fingerprint)
			metric = video.ViewCount
		} else {
			result, err = t.requests.SaveCreator(phase, profile, fingerprint)
			metric, metricName = profile.FollowerCount, "follower"
		}
		if err != nil {
			return errors.Join(ErrTargetStorage, err)
		}
		t.log.Add(Entry{Time: time.Now(), Level: "info", Target: kind + ":" + id, Action: "request_persist", Message: "ok", VersionAdded: result.VersionAdded, Detail: fmt.Sprintf("seq=%d", result.Seq)})
		// 显式申请的指标属于 persist 阶段，失败传回调用方，保留已经提交的资料。
		if metric != nil {
			if err := t.requests.RecordMetric(phase, t.platform, kind, id, metricName, dayWindow(time.Now()), *metric); err != nil {
				return errors.Join(ErrTargetStorage, err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	target := storage.Target{Platform: t.platform, TargetType: kind, TargetID: id, IntervalSeconds: int64(DefaultInterval.Seconds())}
	result := "tracking"
	err = requestStage(writeRoot, t.budgets.Target, "target", "unknown", func(phase context.Context) error {
		inserted, err := t.requests.AddRequestedTarget(phase, target, time.Now())
		if err != nil {
			return errors.Join(ErrTargetStorage, err)
		}
		if !inserted {
			result = "existing"
			current, found, err := t.requests.TrackingTarget(phase, t.platform, kind, id)
			if err != nil {
				return errors.Join(ErrTargetStorage, err)
			}
			if !found {
				return errors.New("target removed after insertion conflict")
			}
			if !current.Enabled {
				return ErrTargetPaused
			}
			return nil
		}
		// 与建任务共用预算；不另开 Background 超时，也不吞掉初始状态记录失败。
		if err := t.requests.RecordTargetResult(phase, target, "ok", time.Now()); err != nil {
			return errors.Join(ErrTargetStorage, err)
		}
		return nil
	})
	return result, err
}
