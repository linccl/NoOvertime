# 安卓端 Token-Only 改造对 API 端总览

## 1. 这份文档的用途

本文档用于把安卓端本轮已经完成并验收通过的内容，整理成一份给 API 端直接查看的总览说明。

适用场景：

- API 端需要了解安卓端当前已经切换到什么口径
- API 端需要知道哪些接口契约必须与安卓端对齐
- API 端需要区分“哪些是必须改的服务端行为”与“哪些只是安卓端 UI 改动”

当前状态：

- 安卓端代码已完成 token-only 改造
- reviewer 已复审通过
- inspector 已验收通过
- 安卓端文档已统一到 token-only 口径

> 对于“身份鉴权、sync/commits、Web 只读查询、配对/恢复/迁移暂停”的当前口径，如与本仓库旧文档示例存在冲突，以本文为准。

## 2. 先说结论

安卓端当前已经完成以下切换：

- 客户端身份模型从 `user_id + device_id + writer_epoch + token` 收敛为 `token + optional client_fingerprint`
- `user_id` 不再是客户端前置输入，也不再参与请求鉴权
- `user_id` 只作为只读缓存存在，且仅在首次同步成功后由服务端响应回填
- `sync/commits` 顶层已移除 `user_id/device_id/writer_epoch`
- Web 月/日汇总查询统一走 `Authorization: Bearer <token>`
- 安卓设置页已移除 `user_id/device_id/writer_epoch` 可编辑输入
- 安卓端对 `USER_ID_NOT_READY` 已统一提示“请先完成一次同步”

另外，自 2026-03-14 起，后端当前阶段已暂停以下旧流程：

- `pairing-code/*`
- `recovery-code/*`
- `web/read-bindings*`
- `migrations/takeover` / `migrations/forced-takeover`

另外：

- 工时展示已改为“X 小时 Y 分”，这是安卓端 UI 改动，不需要 API 端配合改接口

## 3. API 端必须了解并对齐的内容

### 3.1 客户端身份模型

当前安卓端只管理：

- `token`
- `client_fingerprint`（可选）
- `user_id`（只读缓存）

其中：

- `token` 是所有受保护接口的唯一有效身份凭证
- `user_id` 不再由客户端生成，不再让用户输入
- `user_id` 仅在首次同步成功后由服务端生成并回传
- Room 中历史 `device_id` / `writer_epoch` 字段虽然还保留，但只作兼容占位，安卓端已不再把它们当成业务前置条件

### 3.2 受保护接口的统一鉴权方式

以下当前有效接口，安卓端统一按 Bearer token 调用：

- `POST /api/v1/sync/commits`
- `POST /api/v1/migrations/requests`
- `POST /api/v1/migrations/confirm`
- `POST /api/v1/web/month-summaries/query`
- `POST /api/v1/web/day-summaries/query`

统一请求规则：

- 必需 Header：`Authorization: Bearer <token>`
- 建议 Header：`X-Request-ID`
- 可选 Header：`X-Client-Fingerprint`

安卓端已经移除的旧 Header：

- `X-User-ID`
- `X-Device-ID`
- `X-Writer-Epoch`

当前已暂停、不可再作为主流程的接口：

- `POST /api/v1/pairing-code/query`
- `POST /api/v1/pairing-code/reset`
- `POST /api/v1/recovery-code/generate`
- `POST /api/v1/recovery-code/reset`
- `POST /api/v1/web/read-bindings`
- `POST /api/v1/web/read-bindings/auth`
- `POST /api/v1/migrations/takeover`
- `POST /api/v1/migrations/forced-takeover`

## 4. API 端应实现或保持一致的契约

### 4.1 生成 token

建议接口：`POST /api/v1/tokens/issue`

请求：

```json
{
  "client_fingerprint": "optional-stable-install-id"
}
```

响应：

```json
{
  "token": "tok_xxx",
  "user_id": null,
  "token_status": "ANONYMOUS"
}
```

服务端行为要求：

- 仅生成 token
- 不在这个阶段生成 `user_id`
- 若旧表结构存在非空或唯一字段要求，由服务端内部生成兼容值

### 4.2 重置 token

建议接口：`POST /api/v1/tokens/rotate`

请求头：

- `Authorization: Bearer <old_token>`

请求体：

```json
{
  "client_fingerprint": "optional-stable-install-id"
}
```

响应（匿名 token）：

```json
{
  "token": "tok_new",
  "user_id": null,
  "token_status": "ANONYMOUS"
}
```

响应（已绑定用户）：

```json
{
  "token": "tok_new",
  "user_id": "2cf6d0da-9bbf-4fcb-9348-bfe0a4f74a8d",
  "token_status": "BOUND"
}
```

服务端行为要求：

- 若旧 token 尚未绑定 `user_id`：直接替换匿名 token
- 若旧 token 已绑定 `user_id`：替换该 `user_id` 对应 token
- 新 token 生效后，旧 token 必须失效

重要说明：

- 安卓端现在不会用 token `issue/rotate` 响应去改写本地 `user_id`
- 安卓端本地 `user_id` 的唯一有效回填来源是“首次同步成功响应”

### 4.3 同步上报：`sync/commits`

安卓端当前请求体顶层已移除：

- `user_id`
- `device_id`
- `writer_epoch`

安卓端当前顶层保留：

- `sync_id`
- `payload_hash`
- `punch_records`
- `leave_records`
- `day_summaries`
- `month_summaries`

服务端行为要求：

- 通过 `Authorization: Bearer <token>` 识别身份
- 若 token 尚未绑定 `user_id`：在首次同步成功时生成 `user_id`
- 将该 `user_id` 与 token 绑定
- 在成功响应中回传 `user_id`

成功响应建议：

```json
{
  "gate_result": "APPLIED",
  "gate_reason": "APPLIED_WRITE",
  "user_id": "2cf6d0da-9bbf-4fcb-9348-bfe0a4f74a8d",
  "request_id": "req_xxx"
}
```

对安卓端来说，这里的 `user_id` 是当前唯一有效回填来源。

### 4.4 Web 只读查询

服务端当前行为要求：

- 通过 `Authorization: Bearer <token>` 识别身份
- 请求体仅保留查询参数：`year` 或 `month_start`
- 可选 Header：`X-Client-Fingerprint`
- 若 token 尚未绑定 `user_id`，统一返回：`409 USER_ID_NOT_READY`

### 4.5 当前暂停能力

后端当前阶段统一暂停；以下能力仅保留为 historical/paused 路径：

- 配对码查询/重置
- 恢复码生成/重置
- Web binding 创建/鉴权
- takeover / forced-takeover

服务端行为要求：

- 保留原路由
- `POST` 统一返回 `410 FEATURE_PAUSED`
- 不再把这些能力作为当前可用主流程

建议错误响应：

```json
{
  "error_code": "FEATURE_PAUSED",
  "message": "this feature is paused in token-only mode",
  "request_id": "req_xxx"
}
```

安卓端现状：

- 已经把 `USER_ID_NOT_READY` 统一提示为“请先完成一次同步”
- 对已暂停接口，不再作为当前联调目标；若误调，应按 `410 FEATURE_PAUSED` 处理，而不是继续按有效主流程联调

## 5. 数据库兼容要求

这轮改造明确不要求修改安卓端和 API 端已有数据库结构。

服务端兼容策略建议：

- 历史非空字段：自动生成默认值
- 历史唯一字段：通过自增、随机值、时间戳派生等方式保证唯一
- 历史 `device_id` / `writer_epoch`：如果旧逻辑仍要求落库，可只作为内部兼容字段保存
- 这些兼容字段不再要求客户端传入，也不再作为鉴权依据

推荐兜底方式：

- `device_id`：服务端内部生成随机 UUID
- `writer_epoch`：固定为 `1` 或旧模型可接受的最小合法值
- 其他唯一字段：`时间戳 + 随机后缀`

## 6. 安卓端已经完成的配套改造

API 端可以把下面这些视为“安卓侧已准备完成”：

- 设置页已移除 `user_id/device_id/writer_epoch` 编辑能力
- `user_id` 已改为只读展示；未同步前显示“未生成”
- 所有受保护接口的客户端调用方式已改成 Bearer token
- `sync/commits` 的 canonical JSON 与 `payload_hash` 已同步收敛
- `USER_ID_NOT_READY` 已有统一 UI 提示
- token `issue/rotate` 已接入客户端，但不会再写回本地 `user_id`

## 7. 哪些内容 API 端现在不用跟进

以下内容属于安卓端内侧改动，不要求 API 端做对应接口变更：

- 工时展示从“纯分钟”改成“X 小时 Y 分”
- 今日页、日详情、日列表、月汇总的展示格式统一
- 调休分钟仍保留原分钟展示

这部分只影响安卓 UI 与展示层，不影响 API 契约。

## 8. 当前验收状态

安卓端本轮已经完成以下验收：

- token-only 契约：通过
- `sync/commits` 顶层字段与 canonical/payload_hash 收敛：通过
- `user_id` 仅由同步成功响应回填：通过
- Room 表结构与 DB version 保持不变：通过
- 工时展示统一为“X 小时 Y 分”：通过
- 历史文档旧口径清理：通过

因此对 API 端来说，当前可以直接把本文作为联调与改造输入。

## 9. 建议 API 端下一步

建议优先处理顺序：

1. 确认 token-only 身份模型作为当前基线
2. 提供或对齐 `/tokens/issue` 与 `/tokens/rotate`
3. 调整 `/sync/commits` 为 token-only，且在首次成功同步回传 `user_id`
4. 保持 Web 月/日汇总查询走 `Authorization: Bearer <mobile_token>`
5. 保持 pairing/recovery/web binding/takeover 为 paused/historical，并统一返回 `410 FEATURE_PAUSED`
6. 对当前仍有效的匿名 token 受保护接口调用统一返回 `USER_ID_NOT_READY`
7. 在不改 DB 结构的前提下补齐兼容兜底逻辑

## 10. 来源文档

如果需要查看安卓端完整上下文，可参考同机上的安卓仓库文档：

- `/Users/linshiyu/lincc/lincc-project/NoOvertime/noovertime-android/docs/Token鉴权对接文档.md`
- `/Users/linshiyu/lincc/lincc-project/NoOvertime/noovertime-android/docs/Token鉴权改造计划.md`
- `/Users/linshiyu/lincc/lincc-project/NoOvertime/noovertime-android/docs/Token鉴权与工时展示交付总结.md`
- `/Users/linshiyu/lincc/lincc-project/NoOvertime/noovertime-android/docs/Token鉴权与工时展示合并说明.md`

如果只想看结论，优先看本文即可。
