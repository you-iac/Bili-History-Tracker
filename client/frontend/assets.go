<<<<<<< HEAD
// Package frontend 内嵌浏览器界面，由本地 Agent 同源提供，界面资源无需 Node 构建，图片按需直连白名单CDN。
// 侧栏工作台见 docs/client-workspace.md，卡片/详情见 docs/client-catalog.md；平台凭据只经用户主动导入交给 Agent，不写入浏览器存储。
=======
// Package frontend 内嵌浏览器界面，由本地 Agent 同源提供，无需 Node 构建或外部 CDN。
// 侧栏工作台见 docs/client-workspace.md；平台凭据只经用户主动导入交给 Agent，不写入浏览器存储。
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
package frontend

import "embed"

<<<<<<< HEAD
//go:embed index.html app.css catalog.css app.js library.js navigation.js cards.js detail.js images.js trend.mjs view.mjs favicon.svg
=======
//go:embed index.html app.css app.js library.js navigation.js view.mjs favicon.svg
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
var Files embed.FS
