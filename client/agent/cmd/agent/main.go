// Command agent 启动本地浏览器客户端；只监听回环地址，不依赖项目服务器。
// 启动与使用说明见 client/README.md，API 与持久化边界见 docs/client-discovery.md。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/httpapi"
	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/scanner"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8183", "本地回环监听地址")
	data := flag.String("data", "data", "本地状态目录，Windows Cookie 在此加密保存")
	flag.Parse()
	if err := run(*listen, *data); err != nil {
		log.Fatal(err)
	}
}
func run(address, directory string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("监听地址必须是回环 IP，例如 127.0.0.1:8183")
	}
	store, err := localstate.New(directory)
	if err != nil {
		return fmt.Errorf("创建本地数据目录失败: %w", err)
	}
	service, err := scanner.New(bilibili.New(nil), store, 800*time.Millisecond)
	if err != nil {
		return err
	}
	defer service.Close()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("启动本地端口失败: %w", err)
	}
	defer listener.Close()
	handler, err := httpapi.New(service, listener.Addr().String())
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := make(chan error, 1)
	go func() { fail <- server.Serve(listener) }()
	fmt.Printf("Bili History Tracker · 本地客户端\n请在浏览器打开以下完整链接（连接密钥仅本次启动有效）：\nhttp://%s/#token=%s\n", listener.Addr().String(), handler.BootstrapToken())
	select {
	case err := <-fail:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
