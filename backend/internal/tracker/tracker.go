// tracker.go 执行单个目标的查询：调用 Provider、计算指纹、写入存储并记录日志。
// 作者目标还会尝试发现合集视频并自动加入视频跟踪；发现结果不完整时只记录警告。

package tracker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

// DefaultInterval 是未指定时使用的查询周期。
const DefaultInterval = 3 * time.Hour

// maxDiscoverPerRun 限制单次作者查询新增的视频目标数，避免一次请求过多。
const maxDiscoverPerRun = 20

// Querier 是 tracker 需要的查询能力，*source.Service 满足该接口。
type Querier interface {
	GetVideo(ctx context.Context, platform, id string) (source.Video, error)
	GetCreator(ctx context.Context, platform, id string) (source.CreatorProfile, error)
	ListCreatorVideos(ctx context.Context, platform, id string) ([]source.CreatorVideo, error)
}

// Tracker 串行执行查询并落库。同一目标不会并发执行。
type Tracker struct {
	store    *storage.Store
	queries  Querier
	log      *Log
	platform string
	mu       sync.Mutex
	running  map[string]bool
}

// New 创建 tracker。platform 是写入数据库的平台名。
func New(store *storage.Store, queries Querier, log *Log, platform string) *Tracker {
	return &Tracker{
		store:    store,
		queries:  queries,
		log:      log,
		platform: platform,
		running:  make(map[string]bool),
	}
}

// RunTarget 执行一次目标查询；同一目标正在执行时直接跳过。
func (t *Tracker) RunTarget(ctx context.Context, target storage.Target) {
	key := target.TargetType + ":" + target.TargetID
	t.mu.Lock()
	if t.running[key] {
		t.mu.Unlock()
		return
	}
	t.running[key] = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.running, key)
		t.mu.Unlock()
	}()

	switch target.TargetType {
	case "creator":
		t.runCreator(ctx, target)
	case "video":
		t.runVideo(ctx, target)
	}
}

// RunNow 在后台异步执行一次目标查询，并把下次运行时间推后。
func (t *Tracker) RunNow(target storage.Target) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		t.RunTarget(ctx, target)
		interval := time.Duration(target.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = DefaultInterval
		}
		_ = t.store.MarkTargetRun(ctx, target, time.Now(), time.Now().Add(interval))
	}()
}

func (t *Tracker) runVideo(ctx context.Context, target storage.Target) {
	outcome := "query_failed"
	defer func() { t.recordOutcome(target, outcome) }()
	started := time.Now()
	video, err := t.queries.GetVideo(ctx, t.platform, target.TargetID)
	if err != nil {
		outcome = errorMessage(err)
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target),
			Action: "query_video", Message: errorMessage(err), DurationMS: sinceMS(started)})
		return
	}
	fingerprint, err := storage.VideoFingerprint(video.Raw)
	if err != nil {
		outcome = "invalid_payload"
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target),
			Action: "query_video", Message: "invalid_payload", DurationMS: sinceMS(started)})
		return
	}
	result, err := t.store.SaveVideo(ctx, video, fingerprint)
	if err != nil {
		outcome = "store_failed"
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target),
			Action: "query_video", Message: "store_failed", Detail: err.Error(), DurationMS: sinceMS(started)})
		return
	}
	outcome = "ok"
	detail := fmt.Sprintf("seq=%d", result.Seq)
	if video.ViewCount != nil {
		detail += fmt.Sprintf(" view=%d", *video.ViewCount)
	}
	t.log.Add(Entry{Time: started, Level: "info", Target: targetLabel(target),
		Action: "query_video", Message: "ok", Detail: detail,
		DurationMS: sinceMS(started), VersionAdded: result.VersionAdded})
	if video.ViewCount != nil {
		t.recordMetric(ctx, "video", target.TargetID, "view", *video.ViewCount)
	}
}

func (t *Tracker) runCreator(ctx context.Context, target storage.Target) {
	outcome := "query_failed"
	defer func() { t.recordOutcome(target, outcome) }()
	started := time.Now()
	profile, err := t.queries.GetCreator(ctx, t.platform, target.TargetID)
	if err != nil {
		outcome = errorMessage(err)
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target),
			Action: "query_creator", Message: errorMessage(err), DurationMS: sinceMS(started)})
		return
	}
	fingerprint, err := storage.CreatorFingerprint(profile.Raw)
	if err != nil {
		outcome = "invalid_payload"
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target),
			Action: "query_creator", Message: "invalid_payload", DurationMS: sinceMS(started)})
		return
	}
	result, err := t.store.SaveCreator(ctx, profile, fingerprint)
	if err != nil {
		outcome = "store_failed"
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target),
			Action: "query_creator", Message: "store_failed", Detail: err.Error(), DurationMS: sinceMS(started)})
		return
	}
	outcome = "ok"
	detail := fmt.Sprintf("seq=%d name=%s", result.Seq, profile.Name)
	if profile.FollowerCount != nil {
		detail += fmt.Sprintf(" followers=%d", *profile.FollowerCount)
	}
	t.log.Add(Entry{Time: started, Level: "info", Target: targetLabel(target),
		Action: "query_creator", Message: "ok", Detail: detail,
		DurationMS: sinceMS(started), VersionAdded: result.VersionAdded})
	if profile.FollowerCount != nil {
		t.recordMetric(ctx, "creator", target.TargetID, "follower", *profile.FollowerCount)
	}
	t.discoverVideos(ctx, target)
}

// discoverVideos 尝试从公开合集发现视频，并把新视频加入跟踪。
// 发现失败只记录警告，不影响已经完成的作者资料入库。
func (t *Tracker) discoverVideos(ctx context.Context, target storage.Target) {
	started := time.Now()
	videos, err := t.queries.ListCreatorVideos(ctx, t.platform, target.TargetID)
	if err != nil {
		t.log.Add(Entry{Time: started, Level: "warn", Target: targetLabel(target),
			Action: "discover", Message: errorMessage(err), DurationMS: sinceMS(started)})
		return
	}
	interval := time.Duration(target.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = DefaultInterval
	}
	added := 0
	for _, video := range videos {
		if added >= maxDiscoverPerRun {
			break
		}
		exists, err := t.store.HasTarget(ctx, t.platform, "video", video.ID)
		if err != nil {
			t.log.Add(Entry{Time: time.Now(), Level: "error", Target: targetLabel(target),
				Action: "discover", Message: "store_failed", Detail: err.Error()})
			return
		}
		if exists {
			continue
		}
		if err := t.store.AddTarget(ctx, t.platform, "video", video.ID, interval, time.Now()); err != nil {
			t.log.Add(Entry{Time: time.Now(), Level: "error", Target: targetLabel(target),
				Action: "discover", Message: "store_failed", Detail: err.Error()})
			return
		}
		added++
		t.runVideo(ctx, storage.Target{
			Platform: t.platform, TargetType: "video", TargetID: video.ID,
			IntervalSeconds: int64(interval.Seconds()),
		})
	}
	t.log.Add(Entry{Time: started, Level: "info", Target: targetLabel(target),
		Action: "discover", Message: "ok",
		Detail: fmt.Sprintf("found=%d added=%d", len(videos), added), DurationMS: sinceMS(started)})
}

func (t *Tracker) recordMetric(ctx context.Context, subjectType, subjectID, metric string, value int64) {
	if err := t.store.RecordMetric(ctx, t.platform, subjectType, subjectID, metric, dayWindow(time.Now()), value); err != nil {
		t.log.Add(Entry{Time: time.Now(), Level: "warn", Target: subjectType + ":" + subjectID,
			Action: "metric", Message: "store_failed", Detail: err.Error()})
	}
}

func dayWindow(at time.Time) time.Time {
	year, month, day := at.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func targetLabel(target storage.Target) string {
	return target.TargetType + ":" + target.TargetID
}

// errorMessage 只保留错误分类，不把底层网络细节写进日志。
func errorMessage(err error) string {
	var queryError *source.Error
	if errors.As(err, &queryError) {
		return string(queryError.Kind)
	}
	return "query_failed"
}

func sinceMS(started time.Time) int64 {
	return time.Since(started).Milliseconds()
}

// IsRunning 只反映本进程中的在途查询，不把运行中状态持久化而遗留到重启之后。
func (t *Tracker) IsRunning(kind, id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running[kind+":"+id]
}

// recordOutcome 在查询取消时也尝试记录失败；仅使用短时本地事务，不重试上游。
func (t *Tracker) recordOutcome(target storage.Target, outcome string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target.Platform = t.platform
	if err := t.store.RecordTargetResult(ctx, target, outcome, time.Now()); err != nil {
		t.log.Add(Entry{Level: "warn", Target: targetLabel(target), Action: "status", Message: "store_failed"})
	}
}
