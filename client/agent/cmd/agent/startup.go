// startup.go 只在服务已监听后打开完整本地链接；正常输出隐藏连接密钥。
// 禁用或打开失败时保留手动链接，不取消认证。见 docs/client-launch-media.md。
package main

import (
	"fmt"
	"io"
)

func announceStartup(output io.Writer, address, token string, automatic bool, launch func(string) error) {
	base := "http://" + address + "/"
	target := base + "#token=" + token
	fmt.Fprintf(output, "Bili History Tracker · 本地客户端\n本地地址：%s\n", base)
	if automatic && launch(target) == nil {
		fmt.Fprintln(output, "已请求打开默认浏览器，网页将自动连接。")
		return
	}
	if automatic {
		fmt.Fprintln(output, "未能自动打开浏览器。")
	}
	fmt.Fprintf(output, "请手动打开以下完整链接（仅本次启动有效）：\n%s\n", target)
}
