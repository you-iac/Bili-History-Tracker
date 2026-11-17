// limit_test.go 用可控时钟检查滚动窗口、共享额度和慢请求，测试不依赖休眠。
package publicapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateRollingWindowAndSharedPaths(t *testing.T) {
	g := newGate(2)
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	calls := 0
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	request := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("%s: %d, want %d", path, w.Code, want)
		}
		if want == 200 && (w.Header().Get("Retry-After") != "1" || w.Header().Get("Content-Type") != "application/json; charset=utf-8") {
			t.Fatal("missing retry response", w.Header())
		}
	}
	request("/creators", 204)
	now = now.Add(500 * time.Millisecond)
	request("/api/v1/platforms/bilibili/creators/1", 204)
	request("/healthz", 200)
	now = now.Add(499 * time.Millisecond)
	request("/videos", 200)
	now = now.Add(time.Millisecond)
	request("/videos", 204)
	request("/healthz", 200)
	if calls != 3 || g.active != 0 {
		t.Fatal("rejected request reached handler or leaked slot", calls, g.active)
	}
}

func TestGateLimitsConcurrentRequestsAfterWindowExpires(t *testing.T) {
	g := newGate(1)
	var ticks atomic.Int64
	g.now = func() time.Time { return time.Unix(1000, ticks.Load()) }
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; w.WriteHeader(500) }))
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/slow", nil))
	}()
	<-started
	ticks.Store(int64(2 * time.Second))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/other", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"RATE_LIMITED"`) {
		t.Fatalf("slow request lost its slot after time window: %d", w.Code)
	}
	close(release)
	<-done
	if !g.enter() {
		t.Fatal("failed response did not release active slot")
	}
	g.leave()
}

func TestGateConcurrentAdmissionIsAtomic(t *testing.T) {
	g := newGate(5)
	g.now = func() time.Time { return time.Unix(1000, 0) }
	var accepted atomic.Int64
	var group sync.WaitGroup
	for range 100 {
		group.Go(func() {
			if g.enter() {
				accepted.Add(1)
				g.leave()
			}
		})
	}
	group.Wait()
	if accepted.Load() != 5 || g.active != 0 {
		t.Fatal("concurrent requests bypassed limit", accepted.Load(), g.active)
	}
}

func TestGatePanicReleasesActiveSlot(t *testing.T) {
	g := newGate(1)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing handler panic")
			}
		}()
		g.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if g.active != 0 {
		t.Fatal("panic leaked active slot")
	}
}
