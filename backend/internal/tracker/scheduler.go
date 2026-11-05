// scheduler.go 按固定节拍检查到期的跟踪目标并串行执行，避免并发压垮上游。
// 每个目标执行后推进 next_run_at；失败也推进，防止失败目标反复重试。

package tracker

import (
	"context"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

// Scheduler 周期性取出到期目标并交给 Tracker 执行。
type Scheduler struct {
	tracker *Tracker
	store   *storage.Store
	log     *Log
	tick    time.Duration
	limit   int
}

// NewScheduler 创建调度器；tick 是检查间隔，limit 是每轮最多执行的目标数。
func NewScheduler(tracker *Tracker, store *storage.Store, log *Log, tick time.Duration, limit int) *Scheduler {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	if limit <= 0 {
		limit = 10
	}
	return &Scheduler{tracker: tracker, store: store, log: log, tick: tick, limit: limit}
}

// Run 阻塞运行直到 ctx 取消。启动时立即执行一轮，让新添加的目标尽快入库。
func (s *Scheduler) Run(ctx context.Context) {
	s.runDue(ctx)
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx)
		}
	}
}

func (s *Scheduler) runDue(ctx context.Context) {
	targets, err := s.store.DueTargets(ctx, time.Now(), s.limit)
	if err != nil {
		s.log.Add(Entry{Time: time.Now(), Level: "error", Action: "schedule", Message: "store_failed", Detail: err.Error()})
		return
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return
		}
		targetCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		s.tracker.RunTarget(targetCtx, target)
		cancel()

		interval := time.Duration(target.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = DefaultInterval
		}
		if err := s.store.MarkTargetRun(ctx, target, time.Now(), time.Now().Add(interval)); err != nil {
			s.log.Add(Entry{Time: time.Now(), Level: "error", Action: "schedule",
				Message: "store_failed", Detail: err.Error()})
		}
	}
}
