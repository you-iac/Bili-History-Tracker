//go:build !windows

// browser_other.go 使用系统URL打开程序，不拼接Shell命令；子进程异步回收。
// 无图形环境或打开程序缺失时由启动入口显示手动链接。见 docs/client-launch-media.md。
package main

import (
	"os/exec"
	"runtime"
)

func openBrowser(target string) error {
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "open"
	}
	command := exec.Command(program, target)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
