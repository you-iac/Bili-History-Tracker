# 公共 JSON 应答与 Agent 关注核对

状态：已实现，验证记录见关联计划。实施与验证见 [计划](../PLANs/2026-11-17-public-json-response.md)。本协议覆盖 [公共接口](public-api.md) 的旧业务 HTTP 映射；仅影响8181，不改变8182管理及Agent本机会话保护。

## HTTP 与 JSON

正常处理的业务结果统一HTTP200，包括无资料、无任务、暂停、忙、限流、上游不可访问与申请超时。顶层保留成功字段，增加 `ok`、`code`，失败带展示文字 `error`。HTTP200不代表业务成功。非法参数/JSON、体积超限、来源禁止、媒体类型、未知路由/方法保留400/413/403/415/404/405；本服务数据库等内部故障保留500。不能简单替换全部状态码。

| code | 含义与处理 |
|---|---|
| OK | 成功读取或申请，检查目标字段后保存 |
| DETAIL_NOT_RECORDED | 无成功资料，展示缓存/空状态，核对任务 |
| SNAPSHOT_NOT_FOUND | 指定快照缺失，保留已有资料 |
| TARGET_PAUSED | 管理员暂停，不自动恢复 |
| TARGET_BUSY | 同目标处理中，退避后先查询 |
| RATE_LIMITED | 入口/采集容量限制，按retry_after_seconds及Retry-After退避 |
| TARGET_SUBMISSION_FAILED | 无法验证或保存，或最终读取时任务已不存在。先核对状态，再按有界退避重试，不能据此推断下架 |
| REQUEST_TIMEOUT | 结果可能已生效，先核对再申请 |
| UPSTREAM_UNAVAILABLE | 实时上游失败，不当作本服务断连 |

## 目标与接口

统一状态为 `kind/id/tracking_state/tracking/recorded/status`。tracking_state为absent/active/paused；tracking兼容旧布尔字段；recorded表示当前有成功资料，不能区分从未采集与丢失，不能推断视频下架。status继续描述观测状态，提交结果用result=created/existing，不混用。

- GET /api/targets/status：目标未知也以200返回ok:true/code:OK及真实状态，只读不采集。
- POST /targets：提交后确认任务为 `active` 才返回成功。单目标成功返回平铺状态和 `result`，失败返回 `ok:false/code/error`，能够确认时附 `target`。最终状态为 `paused` 或 `absent` 时，分别返回 `TARGET_PAUSED` 或 `TARGET_SUBMISSION_FAILED`，附读到的状态。批量保留前 N 项和原序号，每项按同一规则生成 `ok/code`，旧 `http_status` 保留作诊断。应答描述本次确认时观察到的状态，后续管理操作仍可改变任务状态。
- GET /detail/{type}/{id}：成功保留item/metrics/versions并附target；无资料200+DETAIL_NOT_RECORDED及target；快照缺失200+SNAPSHOT_NOT_FOUND。任务读取失败不伪造absent。
- 指标不存在同样使用DETAIL_NOT_RECORDED；实时查询的上游失败映射为公共JSON业务结果。

示例（HTTP200）：

```json
{
  "ok": false,
  "code": "DETAIL_NOT_RECORDED",
  "error": "服务器暂无该目标的成功观测资料。",
  "target": {
    "kind": "video", "id": "BV17x411w7KC",
    "tracking_state": "absent", "tracking": false,
    "recorded": false, "status": "not_recorded"
  }
}
```

## Agent执行边界

```text
浏览当前页 / 打开详情 / 后台分批核对
  -> 当前服务器精确状态
     -> active：确认；无资料显示等待采集，不重复申请
     -> absent：仍本地关注才申请
     -> paused：停止自动申请，只读核对
     -> 查询失败：保留未知/缓存，退避
```

启动、切服及每30秒后台轮次最多处理3个到期目标，已确认目标每15分钟复核；浏览60秒内复用确认。核对和提交按目标合并，写回校验服务器代际及关注身份；切服、取关、重新关注拒绝旧结果。提交前和超时后先查状态，连续失败6次进入人工处理，手动重试先核对；暂停不自动恢复。重试次数、下次处理、核对时间及错误业务码持久化；follow_identity使用随机标识，避免同一时间戳下取关重关注误接旧响应。摘要/观测时间不充当跟踪核对时间。

摘要精确读取当前页本地目标，避免缺失目标触发公共全库扫描；Agent统一以150毫秒的启动间隔限制请求速率，限流不紧密循环。详情返回read_code/read_error/target/cached；分别显示无资料、限流、业务失败与连接失败。无缓存也展示目标和关注身份，不伪造播放量、版本或下架。

兼容旧成功结构与旧HTTP404/409/429/502；旧404须精确核对，不解析中文推断永久原因。先升级Agent再升级服务器，旧Agent不保证能识别200业务失败。

## 实现导航

[服务器](../backend/internal/publicapi/server.go)、[应答和目标状态](../backend/internal/publicapi/response.go)、[实时兼容](../backend/internal/publicapi/legacy.go)、[协议回归](../backend/internal/publicapi/response_test.go)、[远程解析](../client/agent/internal/remote/client.go)、[关注服务](../client/agent/internal/library/service.go)、[队列](../client/agent/internal/library/pending.go)、[核对实现](../client/agent/internal/library/reconciliation.go)、[核对回归](../client/agent/internal/library/reconciliation_test.go)、[摘要](../client/agent/internal/library/summaries.go)、[详情](../client/frontend/detail.js)。使用模拟上游和临时数据库验证，不操作真实数据库。
