// Command server 组装平台 Provider、查询服务、数据库、后台与循环调度。
//
// 输入：环境变量 BHT_ADDR、BHT_ADMIN_ADDR、BHT_DB、BHT_QUERY_LOG、BHT_BILI_COOKIE，
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
//	│         │ admin.New                  │              │ httpapi.New                │          │
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
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"bili-history-tracker/backend/internal/admin"
	"bili-history-tracker/backend/internal/httpapi"
	"bili-history-tracker/backend/internal/provider/bilibili"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go scheduler.Run(ctx)

	apiServer := &http.Server{
		Addr:              apiAddr,
		Handler:           httpapi.New(queries),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
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
		log.Printf("metadata query server listening on %s", apiAddr)
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
