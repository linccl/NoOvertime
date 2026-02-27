# P0-Batch1 实施计划：补齐核心写路径

## 任务目标

补齐迁移申请、迁移确认、强制接管三个核心写路径接口。

全程留痕。

## 任务背景

当前后端只实现了 sync 路由（POST /api/v1/sync/commits），缺失了 API 契约中要求的其他核心写路径。需要补齐以下接口：

1. POST /api/v1/migrations/requests（迁移申请）
2. POST /api/v1/migrations/{migration_request_id}/confirm（迁移确认）
3. POST /api/v1/migrations/forced-takeover（强制接管）

## 实现要求

### 1. 路由注册

在 server.go 中注册以下路由：
- POST /api/v1/migrations/requests
- POST /api/v1/migrations/{migration_request_id}/confirm
- POST /api/v1/migrations/forced-takeover

### 2. 错误码映射

在 sync_commits.go 的 DB error_key -> API 错误码映射中补充：
- MIGRATION_SOURCE_MISMATCH
- MIGRATION_TRANSITION_INVALID -> MIGRATION_STATE_INVALID
- MIGRATION_IMMUTABLE_FIELDS
- MIGRATION_USER_NOT_FOUND -> USER_NOT_FOUND

### 3. 接口实现规范

参考文档：
- docs/API契约草案.md（2.2-2.4 节，附录 B）
- docs/核心写路径实现清单-T001.md（2.1-2.3 节）

统一约束：
- 统一 JSON 响应；错误响应字段为 error_code、message、request_id
- 请求解析采用白名单字段；未知字段返回 UNKNOWN_FIELD（HTTP 400）
- 参数格式错误/必填缺失返回 INVALID_ARGUMENT（HTTP 400）
- 业务拒绝返回 HTTP 409；限流拒绝返回 HTTP 429

## 验收标准

1. 三个路由已注册并可访问
2. 错误码映射已补充完整
3. 接口实现符合 API 契约草案规范
4. 所有错误响应包含 error_code、message、request_id
5. 限流策略正确实现（MIGRATION_REQUEST、MIGRATION_CONFIRM、RECOVERY_VERIFY）

## 参考文档

- /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/API契约草案.md
- /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/核心写路径实现清单-T001.md
- /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/需求文档.md
