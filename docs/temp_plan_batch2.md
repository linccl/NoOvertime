# P1-Batch2 实施计划：补齐配对码和恢复码路径

## 任务目标

补齐配对码查询、配对码重置、恢复码生成、恢复码重置、Web 只读绑定创建、Web 只读绑定鉴权六个核心写路径接口。

全程留痕。

## 任务背景

当前后端已完成迁移相关的 3 个接口，但还缺少配对码和恢复码相关的 6 个接口：

1. POST /api/v1/pairing-code/query（配对码查询/首次生成）
2. POST /api/v1/pairing-code/reset（配对码重置）
3. POST /api/v1/recovery-code/generate（恢复码首次生成）
4. POST /api/v1/recovery-code/reset（恢复码重置）
5. POST /api/v1/web/read-bindings（Web 只读绑定创建）
6. POST /api/v1/web/read-bindings/auth（Web 只读绑定鉴权）

## 实现要求

### 1. 路由注册

在 server.go 中注册以下路由：
- POST /api/v1/pairing-code/query
- POST /api/v1/pairing-code/reset
- POST /api/v1/recovery-code/generate
- POST /api/v1/recovery-code/reset
- POST /api/v1/web/read-bindings
- POST /api/v1/web/read-bindings/auth

### 2. 错误码映射

补充以下错误码映射：
- PAIRING_CODE_GENERATE_FAILED
- RECOVERY_CODE_ALREADY_INITIALIZED
- RECOVERY_CODE_INVALID
- WEB_BINDING_REACTIVATE_DENIED
- WEB_BINDING_VERSION_MISMATCH
- WEB_BINDING_VERSION_IMMUTABLE
- WEB_BINDING_USER_IMMUTABLE
- UNAUTHORIZED_DEVICE
- UNAUTHORIZED_WEB_TOKEN

### 3. 接口实现规范

参考文档：
- docs/API契约草案.md（2.5-2.10 节，附录 B）
- docs/核心写路径实现清单-T001.md（2.4-2.9 节）

统一约束：
- 统一 JSON 响应；错误响应字段为 error_code、message、request_id
- 请求解析采用白名单字段；未知字段返回 UNKNOWN_FIELD（HTTP 400）
- 参数格式错误/必填缺失返回 INVALID_ARGUMENT（HTTP 400）
- 业务拒绝返回 HTTP 409；限流拒绝返回 HTTP 429

## 验收标准

1. 六个路由已注册并可访问
2. 错误码映射已补充完整
3. 接口实现符合 API 契约草案规范
4. 所有错误响应包含 error_code、message、request_id
5. 限流策略正确实现（PAIRING_RESET、RECOVERY_VERIFY、WEB_PAIR_BIND）

## 参考文档

- /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/API契约草案.md
- /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/核心写路径实现清单-T001.md
- /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/需求文档.md
