// Package frontend 内嵌浏览器界面，由本地 Agent 同源提供，界面资源无需 Node 构建，图片按需直连白名单CDN。
// 侧栏工作台见 docs/client-workspace.md，卡片/详情见 docs/client-catalog.md；平台凭据只经用户主动导入交给 Agent，不写入浏览器存储。
package frontend

import "embed"

//go:embed index.html app.css catalog.css app.js library.js history.js navigation.js cards.js detail.js detail-sources.mjs creator-videos.js images.js trend.mjs view.mjs favicon.svg
var Files embed.FS
