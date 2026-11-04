// Command server 组装平台 Provider、查询服务和 HTTP 路由，并管理监听与关闭流程。
// 本入口负责读取服务配置，具体平台的数据获取由 Provider 实现。
//
// 设计说明（仓库相对路径）：docs/architecture.md#module-navigation。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"bili-history-tracker/backend/internal/httpapi"
	"bili-history-tracker/backend/internal/provider/bilibili"
	"bili-history-tracker/backend/internal/source"
)

func main() {
	queries, err := source.NewService(bilibili.New())
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("BHT_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8181"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(queries),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
			_ = server.Close()
		}
	}()
	log.Printf("metadata query server listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	stop()
	<-done
}
