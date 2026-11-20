// tracker.go 执行单个目标的查询：调用 Provider、计算指纹、写入存储并记录日志。
// 作者资料采集与视频关注独立；只有用户显式选择的视频才进入跟踪。

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

// Querier 是 tracker 需要的查询能力，*source.Service 满足该接口。
type Querier interface {
	GetVideo(ctx context.Context, platform, id string) (source.Video, error)
	GetCreator(ctx context.Context, platform, id string) (source.CreatorProfile, error)
}

// Tracker 对同一目标互斥执行；调度器串行，显式申请可有限并发。
// Stop/Wait 管理在途工作；已接收有效资料的申请使用独立预算保存，见 docs/request-stages.md。
type Tracker struct {
	store    *storage.Store
	requests requestStore
	budgets  RequestBudgets
	queries  Querier
	log      *Log
	platform string
	mu       sync.Mutex
	running  map[string]bool
	stopping bool
	workers  sync.WaitGroup
	workCtx  context.Context
	stopWork context.CancelFunc
}

// New 创建 tracker。platform 是写入数据库的平台名。
func New(store *storage.Store, queries Querier, log *Log, platform string) *Tracker {
	workCtx, stopWork := context.WithCancel(context.Background())
	return &Tracker{
		store:    store,
		requests: store,
		budgets:  DefaultRequestBudgets(),
		queries:  queries,
		log:      log,
		platform: platform,
		running:  make(map[string]bool),
		workCtx:  workCtx,
		stopWork: stopWork,
	}
}

// RunTarget 执行一次目标查询；同一目标正在执行时直接跳过。
func (t *Tracker) RunTarget(ctx context.Context, target storage.Target) {
	if t.beginTarget(target.TargetType, target.TargetID, false) != nil {
		return
	}
	defer t.endTarget(target.TargetType, target.TargetID)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.workCtx, cancel)
	defer stop()
	defer cancel()
	t.runTarget(ctx, target)
}

func (t *Tracker) runTarget(ctx context.Context, target storage.Target) {
	switch target.TargetType {
	case "creator":
		t.runCreator(ctx, target)
	case "video":
		t.runVideo(ctx, target)
	}
}

// RunNow 在后台异步执行一次目标查询，并把下次运行时间推后。
func (t *Tracker) RunNow(target storage.Target) {
	// 启动 goroutine 前登记，Stop/Wait 不会漏过尚未获得运行机会的任务。
	if t.beginTarget(target.TargetType, target.TargetID, false) != nil {
		return
	}
	go func() {
		defer t.endTarget(target.TargetType, target.TargetID)
		ctx, cancel := context.WithTimeout(t.workCtx, 2*time.Minute)
		defer cancel()
		t.runTarget(ctx, target)
		interval := time.Duration(target.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = DefaultInterval
		}
		_ = t.store.MarkTargetRun(ctx, target, time.Now(), time.Now().Add(interval))
	}()
}

// Stop 停止接纳新的 tracker 工作并取消查询；已进入独立保存阶段的显式申请继续有界收尾。
// 必须在关闭数据库前调用 Stop 和 Wait；HTTP handler 的最终确认另由 HTTP Shutdown 等待。
func (t *Tracker) Stop() {
	t.mu.Lock()
	t.stopping = true
	t.stopWork()
	t.mu.Unlock()
}

// Wait 等待已登记的采集和显式申请。先调用 Stop，防止等待期间继续登记工作。
func (t *Tracker) Wait() { t.workers.Wait() }

func (t *Tracker) beginTarget(kind, id string, limited bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopping {
		return ErrRequestStopping
	}
	key := kind + ":" + id
	if t.running[key] {
		return ErrTargetBusy
	}
	if limited && len(t.running) >= 16 {
		return ErrRequestCapacity
	}
	t.running[key] = true
	t.workers.Add(1)
	return nil
}

func (t *Tracker) endTarget(kind, id string) {
	t.mu.Lock()
	delete(t.running, kind+":"+id)
	t.mu.Unlock()
	t.workers.Done()
}

func (t *Tracker) runVideo(ctx context.Context, target storage.Target) {
	outcome := "query_failed"
	defer func() { t.recordOutcome(target, outcome) }()
	started := time.Now()
	video, err := t.queries.GetVideo(ctx, t.platform, target.TargetID)
	if err == nil {
		_, err = t.persistVideo(ctx, target, video)
	}
	if err != nil {
		outcome = errorMessage(err)
		t.log.Add(Entry{Time: started, Level: "error", Target: targetLabel(target), Action: "query_video", Message: outcome, DurationMS: sinceMS(started)})
		return
	}
	outcome = "ok"
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
