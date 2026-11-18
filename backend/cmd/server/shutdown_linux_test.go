//go:build linux

// shutdown_linux_test.go 用隔离子进程验证 Linux SIGINT／SIGTERM 进入现有停机路径。
// 只覆盖空闲服务的信号入口，不代表活动请求和后台任务已完成停机验收。
package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServerShutdownSignals(t *testing.T) {
	if os.Getenv("BHT_TEST_SERVER_SHUTDOWN") == "1" {
		main()
		return
	}
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestServerShutdownSignals$")
			dir := t.TempDir()
			cmd.Env = append(os.Environ(),
				"BHT_TEST_SERVER_SHUTDOWN=1",
				"BHT_ADDR=127.0.0.1:0", "BHT_ADMIN_ADDR=127.0.0.1:0",
				"BHT_API_RPS=20", "BHT_API_BATCH_MAX=10", "BHT_BILI_COOKIE=",
				"BHT_DB="+filepath.Join(dir, "test.db"),
				"BHT_QUERY_LOG="+filepath.Join(dir, "query.log"))
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			type result struct {
				log string
				err error
			}
			ready := make(chan struct{})
			done := make(chan result, 1)
			go func() {
				var log strings.Builder
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					line := scanner.Text()
					log.WriteString(line + "\n")
					// 该日志在注册信号之后输出，用它同步，避免靠 sleep 猜测。
					if strings.Contains(line, "admin console listening on ") {
						close(ready)
					}
				}
				scanErr := scanner.Err()
				waitErr := cmd.Wait()
				if scanErr != nil {
					waitErr = scanErr
				}
				done <- result{log.String(), waitErr}
			}()
			select {
			case <-ready:
			case r := <-done:
				t.Fatalf("server exited before signal: %v\n%s", r.err, r.log)
			case <-ctx.Done():
				r := <-done
				t.Fatalf("server startup timed out: %v\n%s", r.err, r.log)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				cancel()
				r := <-done
				t.Fatalf("send signal: %v\n%s", err, r.log)
			}
			r := <-done
			if ctx.Err() != nil || r.err != nil || !strings.Contains(r.log, "database closed, scheduler stopped") {
				t.Fatalf("signal did not complete existing shutdown path: context=%v, exit=%v\n%s", ctx.Err(), r.err, r.log)
			}
		})
	}
}
