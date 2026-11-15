// Package frontend 内嵌浏览器界面，由本地 Agent 同源提供，无需 Node 构建或外部 CDN。
// 侧栏工作台见 docs/client-workspace.md，卡片/详情见 docs/client-catalog.md；平台凭据只经用户主动导入交给 Agent，不写入浏览器存储。
package frontend

import "embed"

//go:embed index.html app.css catalog.css app.js library.js navigation.js cards.js detail.js trend.mjs view.mjs favicon.svg
var Files embed.FS
