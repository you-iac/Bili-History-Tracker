// browser_windows.go 通过Windows默认URL处理程序打开网页，不经过命令行Shell。
// 用户主动启动客户端时显示浏览器；本地认证仍使用链接片段。见 docs/client-launch-media.md。
package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var shellExecute = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
var ole32 = syscall.NewLazyDLL("ole32.dll")
var initializeCOM = ole32.NewProc("CoInitializeEx")
var uninitializeCOM = ole32.NewProc("CoUninitialize")

func openBrowser(target string) error {
	// URL处理程序可能使用COM扩展，初始化与调用保持在同一OS线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized, _, _ := initializeCOM.Call(0, 2)
	if uint32(initialized) <= 1 {
		defer uninitializeCOM.Call()
	}
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	address, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, _ := shellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(address)), 0, 0, 1)
	runtime.KeepAlive(verb)
	runtime.KeepAlive(address)
	if result <= 32 {
		return fmt.Errorf("默认浏览器打开失败（系统代码%d）", result)
	}
	return nil
}
