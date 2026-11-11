// Package frontend 内嵌浏览器界面，由本地 Agent 同源提供，无需 Node 构建或外部 CDN。
// 平台凭据只经用户主动导入交给 Agent，不写入浏览器存储。见 docs/client-discovery.md。
package frontend

import "embed"

//go:embed index.html app.css app.js view.mjs favicon.svg
var Files embed.FS
