// Command server 组装平台 Provider、查询服务、数据库、后台与循环调度。
//
// 输入：环境变量 BHT_ADDR、BHT_ADMIN_ADDR、BHT_API_RPS、BHT_API_BATCH_MAX、
// BHT_DB、BHT_QUERY_LOG、BHT_BILI_COOKIE，
// 以及操作系统的中断信号。
// 输出：两个 HTTP 监听端口、一个调度 goroutine、一个 SQLite 文件；退出时按序关闭。
// 它不认识：业务规则、SQL、B站字段；只负责创建对象、连接依赖和管理生命周期。
//
// 上下游关系：
//
//	                                ┌────────────────────────────┐
//	                                │ main.go                    │
//	                                │ 配置 / 对象组装 / 生命周期 │
//	                                └────────────────────────────┘
//	                                               │
//	              创建并连接依赖                   │
//	┌──────────────────────────────────────────────┴──────────────────────────────────────────────┐
//	│                                                                                             │
//	│         ┌────────────────────────────┐              ┌────────────────────────────┐          │
//	│         │ storage.Open               │              │ source.NewService          │          │
//	├────────►│ SQLite 连接 / 迁移         │              │ 注册 bilibili Provider     │◄─────────┤
//	│         └────────────────────────────┘              └────────────────────────────┘          │
//	│                                                                                             │
//	│                                                                                             │
//	│                                                                                             │
//	│                                                                                             │
//	│         ┌────────────────────────────┐              ┌────────────────────────────┐          │
//	│         │ tracker.NewLog             │              │ tracker + Scheduler        │          │
//	├────────►│ 内存日志 / query.log       │              │ 目标执行 / 循环调度        │◄─────────┤
//	│         └────────────────────────────┘              └────────────────────────────┘          │
//	│                                                                                             │
//	│                                                                                             │
//	│                                                                                             │
//	│                                                                                             │
//	│         ┌────────────────────────────┐              ┌────────────────────────────┐          │
//	│         │ admin.New                  │              │ publicapi.New              │          │
//	└────────►│ HTML 后台  :8182           │              │ JSON API  :8181            │◄─────────┘
//	          └────────────────────────────┘              └────────────────────────────┘
//
//	                                 输入：环境变量 / 信号
//
// 对外查询 API 默认监听 127.0.0.1:8181，后台默认监听 127.0.0.1:8182。
// 数据库路径由 BHT_DB 指定，查询日志由 BHT_QUERY_LOG 指定。
// 设计说明（仓库相对路径）：docs/architecture.md#module-navigation。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/admin"
	"bili-history-tracker/backend/internal/provider/bilibili"
	"bili-history-tracker/backend/internal/publicapi"
	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

func main() {
	queries, err := source.NewService(bilibili.New())
	if err != nil {
		log.Fatal(err)
	}

	apiAddr := env("BHT_ADDR", "127.0.0.1:8181")
	adminAddr := env("BHT_ADMIN_ADDR", "127.0.0.1:8182")
	apiLimits, err := publicLimits()
	if err != nil {
		log.Fatal(err)
	}
	dbPath := env("BHT_DB", filepath.Join("data", "bht.db"))
	logPath := env("BHT_QUERY_LOG", filepath.Join(filepath.Dir(dbPath), "query.log"))

	store, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = store.Close() }()

	queryLog, err := tracker.NewLog(logPath, 500)
	if err != nil {
		log.Fatalf("open query log: %v", err)
	}
	defer func() { _ = queryLog.Close() }()

	trk := tracker.New(store, queries, queryLog, "bilibili")
	scheduler := tracker.NewScheduler(trk, store, queryLog, 30*time.Second, 10)

	adminHandler, err := admin.New(store, trk, queryLog, "bilibili")
	if err != nil {
		log.Fatalf("build admin handler: %v", err)
	}
	defer adminHandler.Close()
	publicHandler, err := publicapi.New(store, trk, queries, "bilibili", apiLimits)
	if err != nil {
		log.Fatalf("build public API: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go scheduler.Run(ctx)

	apiServer := &http.Server{
		Addr:              apiAddr,
		Handler:           publicHandler.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	adminServer := &http.Server{
		Addr:              adminAddr,
		Handler:           adminHandler.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("public API listening on http://%s/ (RPS=%d, batch=%d)", apiAddr, apiLimits.RequestsPerSecond, apiLimits.BatchMax)
		if err := apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		log.Printf("admin console listening on http://%s/", adminAddr)
		if err := adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		log.Printf("server stopped: %v", err)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = apiServer.Shutdown(shutdownCtx)
	_ = adminServer.Shutdown(shutdownCtx)
	log.Printf("database closed, scheduler stopped")
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// publicLimits 对显式误配置失败退出，不将零或非法输入理解为关闭限制。
func publicLimits() (publicapi.Config, error) {
	cfg := publicapi.Config{RequestsPerSecond: 20, BatchMax: 10}
	for _, option := range []struct {
		key    string
		limit  int
		target *int
	}{
		{"BHT_API_RPS", 10000, &cfg.RequestsPerSecond},
		{"BHT_API_BATCH_MAX", 100, &cfg.BatchMax},
	} {
		if raw, exists := os.LookupEnv(option.key); exists {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > option.limit {
				return cfg, fmt.Errorf("%s must be between 1 and %d", option.key, option.limit)
			}
			*option.target = n
		}
	}
	return cfg, nil
}
