// Command server 组装平台 Provider、查询服务、数据库、后台与循环调度。
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
