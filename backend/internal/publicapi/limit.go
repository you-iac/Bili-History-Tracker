// limit.go 用一个进程级门控制滚动一秒请求数与在途请求数，不排队或创建每 IP 状态。
// 两个上限共用一个配置值；慢请求不能通过等待时间窗口刷新无限堆积。见 docs/public-api.md。
package publicapi

import (
	"net/http"
	"sync"
	"time"
)

type gate struct {
	mu            sync.Mutex
	starts        []time.Time
	limit, active int
	now           func() time.Time
}

func newGate(limit int) *gate { return &gate{limit: limit, now: time.Now} }

// enter 的检查与预留在同一个锁内完成；拒绝不计入已接纳次数。
func (g *gate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	i := 0
	for i < len(g.starts) && !g.starts[i].After(now.Add(-time.Second)) {
		i++
	}
	if i > 0 {
		g.starts = append(g.starts[:0], g.starts[i:]...)
	}
	if g.active >= g.limit || len(g.starts) >= g.limit {
		return false
	}
	g.starts = append(g.starts, now)
	g.active++
	return true
}

func (g *gate) leave() {
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
}

func (g *gate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.enter() {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, 429, map[string]string{"error": "服务器请求较多，请稍后重试。"})
			return
		}
		defer g.leave()
		next.ServeHTTP(w, r)
	})
}
