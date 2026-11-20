// profiling.go 提供默认关闭的独立回环诊断入口，承载 pprof 和执行 trace。
// 不使用公共或管理路由；采样仅由显式配置开启。使用与解释见 docs/performance-profiling.md。
package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"time"
)

type profilingConfig struct {
	addr          string
	blockRate     int
	mutexFraction int
}

func profilingOptions() (profilingConfig, error) {
	cfg := profilingConfig{addr: os.Getenv("BHT_PPROF_ADDR")}
	for _, option := range []struct {
		key    string
		target *int
	}{
		{"BHT_PPROF_BLOCK_RATE", &cfg.blockRate},
		{"BHT_PPROF_MUTEX_FRACTION", &cfg.mutexFraction},
	} {
		if raw := os.Getenv(option.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return cfg, fmt.Errorf("%s must be a non-negative integer", option.key)
			}
			*option.target = n
		}
	}
	if cfg.addr == "" {
		if cfg.blockRate != 0 || cfg.mutexFraction != 0 {
			return cfg, fmt.Errorf("profiling sampling requires BHT_PPROF_ADDR")
		}
		return cfg, nil
	}
	host, port, err := net.SplitHostPort(cfg.addr)
	if err != nil {
		return cfg, fmt.Errorf("BHT_PPROF_ADDR: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return cfg, fmt.Errorf("BHT_PPROF_ADDR must use a loopback IP, for example 127.0.0.1:6060")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return cfg, fmt.Errorf("BHT_PPROF_ADDR port must be between 0 and 65535")
	}
	return cfg, nil
}

// enableSampling 仅在启动已启用的诊断服务时调用一次；0 保留默认关闭的阻塞/锁采样。
// 阻塞采样参数是纳秒间隔，锁采样参数是平均每 N 次竞争采一次，不是时间。
func (cfg profilingConfig) enableSampling() {
	runtime.SetBlockProfileRate(cfg.blockRate)
	runtime.SetMutexProfileFraction(cfg.mutexFraction)
}

func newProfilingServer(cfg profilingConfig) *http.Server {
	if cfg.addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return &http.Server{
		Addr:              cfg.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		// CPU/trace 由 seconds 参数决定时长，不套用业务入口的 50 秒写入时限。
	}
}
