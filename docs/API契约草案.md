# NoOvertime API契约草案

> 状态：Draft v0.1  
> 任务：TASK-03（步骤2：API 契约草案）  
> 基线文档：`docs/需求文档.md`、`docs/数据库方案草案.md`、`docs/实施计划.md`、`db/migrations/001_init.sql`

## 1. 通用约定

### 1.1 基础信息

- Base URL：`/api/v1`
- 数据格式：`application/json; charset=utf-8`
- 时间格式：ISO8601 UTC（示例：`2026-02-12T01:23:00Z`）
- 所有写接口在入库前执行分钟截断（向下取整），详见“附录 G”

### 1.2 鉴权模型

- `DeviceAuth`：移动端写接口，`Authorization: Bearer <device_access_token>`
- `WriterDeviceOnly`：必须是当前写入端（`device_id == users.writer_device_id` 且 `writer_epoch == users.writer_epoch`）
- `WebBindingToken`：Web 只读令牌，`Authorization: Bearer <web_binding_token>`
- `Anonymous`：无需登录，但仍需限流与设备指纹

### 1.3 通用错误响应

```json
{
  "error_code": "RATE_LIMIT_BLOCKED",
  "message": "too many attempts",
  "request_id": "8ea8b7b5-0f6f-43c1-ad03-6528cafc9ef1"
}
```

### 1.4 限流策略总表（MVP 固定值）

| scene | 细粒度阈值 | 同主体全局阈值 | 终端全局阈值（MVP=同主体全局） | 阻断时长 |
|---|---|---|---|---|
| `WEB_PAIR_BIND` | 5 次 / 10 分钟 | 15 次 / 10 分钟 | 15 次 / 10 分钟 | 30 分钟（细粒度）/ 2 小时（全局） |
| `RECOVERY_VERIFY` | 3 次 / 24 小时 | 5 次 / 24 小时 | 5 次 / 24 小时 | 72 小时（细粒度）/ 7 天（全局） |
| `MIGRATION_REQUEST` | 5 次 / 10 分钟 | 12 次 / 10 分钟 | 12 次 / 10 分钟 | 30 分钟（细粒度）/ 2 小时（全局） |
| `MIGRATION_CONFIRM` | 6 次 / 10 分钟 | 15 次 / 10 分钟 | 15 次 / 10 分钟 | 30 分钟（细粒度）/ 2 小时（全局） |
| `PAIRING_RESET` | 3 次 / 24 小时 | 5 次 / 24 小时 | 5 次 / 24 小时 | 24 小时（细粒度）/ 72 小时（全局） |

并行统计三层键：

- `scene + subject_hash + client_fingerprint_hash`
- `scene + subject_hash + GLOBAL`
- `scene + GLOBAL + client_fingerprint_hash`

---

## 2. 接口定义（10项）

### 2.1 同步上报（Punch/LeaveRecord/DaySummary/MonthSummary 原子提交）

- Method & Path：`POST /api/v1/sync/commits`
- 幂等字段：`sync_id` + `payload_hash`
- 鉴权要求：`DeviceAuth + WriterDeviceOnly`
- 限流策略：通用写入限流（网关层），并叠加写入端校验（`SYNC_COMMIT_STALE_WRITER`）

请求 JSON 示例：

```json
{
  "user_id": "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
  "device_id": "0b854f80-0213-4cb1-b5d0-95af02f137f3",
  "writer_epoch": 12,
  "sync_id": "bb5166cb-13ed-47a0-9fb5-58e2062a3559",
  "payload_hash": "3619f3c484d26f31d1942436d2c3010f9a11f706f5f554d224595b3db6b7559d",
  "punch_records": [
    {
      "id": "4acb45c8-65cb-4e20-9602-2ac3609d5c28",
      "local_date": "2026-02-12",
      "type": "START",
      "at_utc": "2026-02-12T01:10:00Z",
      "timezone_id": "Asia/Shanghai",
      "minute_of_day": 550,
      "source": "MANUAL",
      "deleted_at": null,
      "version": 3
    },
    {
      "id": "dd833a85-94d8-42a2-80d7-feeb98698f9d",
      "local_date": "2026-02-12",
      "type": "END",
      "at_utc": "2026-02-12T10:20:00Z",
      "timezone_id": "Asia/Shanghai",
      "minute_of_day": 1100,
      "source": "MANUAL",
      "deleted_at": null,
      "version": 3
    }
  ],
  "leave_records": [
    {
      "id": "1fc35956-0015-4aa7-a0aa-3ef6576fc423",
      "local_date": "2026-02-11",
      "leave_type": "AM",
      "deleted_at": null,
      "version": 2
    }
  ],
  "day_summaries": [
    {
      "id": "3cf42a4f-8107-49dd-96bd-1cd7ea6f3f54",
      "local_date": "2026-02-12",
      "start_at_utc": "2026-02-12T01:10:00Z",
      "end_at_utc": "2026-02-12T10:20:00Z",
      "is_leave_day": false,
      "leave_type": null,
      "is_late": true,
      "work_minutes": 550,
      "adjust_minutes": 0,
      "status": "COMPUTED",
      "version": 5,
      "updated_at": "2026-02-12T10:21:00Z"
    }
  ],
  "month_summaries": [
    {
      "id": "445f1f36-cf1c-4f90-9fd0-b56438e2df2e",
      "month_start": "2026-02-01",
      "work_minutes_total": 6120,
      "adjust_minutes_balance": 120,
      "version": 5,
      "updated_at": "2026-02-12T10:21:00Z"
    }
  ]
}
```

响应 JSON 示例（写入生效）：

```json
{
  "request_id": "f6ed4994-4bf4-4726-af57-a04506eedda0",
  "gate_result": "APPLIED",
  "gate_reason": "APPLIED_WRITE",
  "sync_commit": {
    "sync_id": "bb5166cb-13ed-47a0-9fb5-58e2062a3559",
    "status": "APPLIED",
    "created_at": "2026-02-12T10:21:00Z"
  }
}
```

响应 JSON 示例（幂等重放 NOOP）：

```json
{
  "request_id": "a51cb77e-8b66-40a1-9965-207f58d7c314",
  "gate_result": "NOOP",
  "gate_reason": "REPLAY_NOOP",
  "sync_commit": {
    "sync_id": "bb5166cb-13ed-47a0-9fb5-58e2062a3559",
    "status": "APPLIED",
    "created_at": "2026-02-12T10:21:00Z"
  }
}
```

响应 JSON 示例（冲突拒绝）：

```json
{
  "error_code": "SYNC_ID_CONFLICT",
  "message": "same sync_id but different payload_hash",
  "gate_result": "REJECTED",
  "gate_reason": "SYNC_ID_CONFLICT",
  "request_id": "0a9f1329-86df-4c41-b6d0-75cd89593a3b"
}
```

错误码列表：

- `INVALID_ARGUMENT`
- `UNKNOWN_FIELD`
- `SYNC_ID_CONFLICT`
- `LOW_OR_EQUAL_VERSION_NOOP`（HTTP 200，非失败）
- `PUNCH_END_REQUIRES_START`
- `PUNCH_END_NOT_AFTER_START`
- `CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE`
- `TIME_PRECISION_INVALID`
- `TIME_FIELDS_MISMATCH`
- `STALE_WRITER_REJECTED`

### 2.2 迁移申请（普通迁移）

- Method & Path：`POST /api/v1/migrations/requests`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth`（新设备）+ `WriterDeviceOnly`（`from_device_id` 必须匹配当前 writer）
- 限流策略：`MIGRATION_REQUEST`

请求 JSON 示例：

```json
{
  "user_id": "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
  "from_device_id": "0b854f80-0213-4cb1-b5d0-95af02f137f3",
  "to_device_id": "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
  "mode": "NORMAL",
  "expires_at": "2026-02-12T11:00:00Z"
}
```

响应 JSON 示例：

```json
{
  "migration_request_id": "f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60",
  "status": "PENDING",
  "mode": "NORMAL",
  "expires_at": "2026-02-12T11:00:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `MIGRATION_SOURCE_MISMATCH`
- `MIGRATION_PENDING_EXISTS`
- `MIGRATION_STATE_INVALID`
- `MIGRATION_IMMUTABLE_FIELDS`
- `INVALID_ARGUMENT`

### 2.3 迁移确认

- Method & Path：`POST /api/v1/migrations/{migration_request_id}/confirm`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth + WriterDeviceOnly`（旧机确认）
- 限流策略：`MIGRATION_CONFIRM`

请求 JSON 示例：

```json
{
  "action": "CONFIRM",
  "operator_device_id": "0b854f80-0213-4cb1-b5d0-95af02f137f3"
}
```

响应 JSON 示例：

```json
{
  "migration_request_id": "f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60",
  "status": "COMPLETED",
  "writer_device_id": "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
  "writer_epoch": 13,
  "revoked_device_id": "0b854f80-0213-4cb1-b5d0-95af02f137f3",
  "completed_at": "2026-02-12T10:26:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `MIGRATION_STATE_INVALID`
- `MIGRATION_EXPIRED`
- `MIGRATION_SOURCE_MISMATCH`
- `STALE_WRITER_REJECTED`

### 2.4 强制接管（配对码 + 恢复码）

- Method & Path：`POST /api/v1/migrations/forced-takeover`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth`（新设备会话）+ `Anonymous` 输入 `pairing_code`、`recovery_code`
- 限流策略：先走 `RECOVERY_VERIFY`，再走 `MIGRATION_REQUEST`

请求 JSON 示例：

```json
{
  "pairing_code": "39481726",
  "recovery_code": "AB12CD34EF56GH78",
  "to_device_id": "f2df11ef-7240-42b2-8ceb-623ad7711e0c"
}
```

响应 JSON 示例：

```json
{
  "migration_request_id": "ac5af84e-c497-4344-8994-9fef4ec54ab0",
  "status": "COMPLETED",
  "mode": "FORCED",
  "writer_device_id": "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
  "writer_epoch": 14,
  "completed_at": "2026-02-12T10:30:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `PAIRING_CODE_FORMAT_INVALID`
- `PAIRING_CODE_INVALID`
- `RECOVERY_CODE_INVALID`
- `MIGRATION_STATE_INVALID`

### 2.5 配对码查询（含首次生成）

- Method & Path：`POST /api/v1/pairing-code/query`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth + WriterDeviceOnly`
- 限流策略：通用写入限流（网关层）

请求 JSON 示例：

```json
{
  "ensure_generated": true
}
```

响应 JSON 示例：

```json
{
  "pairing_code": "39481726",
  "pairing_code_version": 3,
  "pairing_code_updated_at": "2026-02-12T08:00:00Z",
  "is_newly_generated": false
}
```

错误码列表：

- `UNAUTHORIZED_DEVICE`
- `STALE_WRITER_REJECTED`
- `USER_NOT_FOUND`

### 2.6 配对码重置

- Method & Path：`POST /api/v1/pairing-code/reset`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth + WriterDeviceOnly`
- 限流策略：`PAIRING_RESET`

请求 JSON 示例：

```json
{
  "reason": "USER_INITIATED"
}
```

响应 JSON 示例：

```json
{
  "pairing_code": "24069175",
  "pairing_code_version": 4,
  "pairing_code_updated_at": "2026-02-12T10:32:00Z",
  "revoked_bindings_count": 2
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `PAIRING_CODE_GENERATE_FAILED`
- `USER_NOT_FOUND`
- `STALE_WRITER_REJECTED`

### 2.7 恢复码首次生成

- Method & Path：`POST /api/v1/recovery-code/generate`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth + WriterDeviceOnly`
- 限流策略：`RECOVERY_VERIFY`

请求 JSON 示例：

```json
{
  "require_first_time": true
}
```

响应 JSON 示例：

```json
{
  "recovery_code": "AB12CD34EF56GH78",
  "recovery_code_masked": "AB12********GH78",
  "shown_once": true,
  "updated_at": "2026-02-12T10:33:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `RECOVERY_CODE_ALREADY_INITIALIZED`
- `STALE_WRITER_REJECTED`

### 2.8 恢复码重置

- Method & Path：`POST /api/v1/recovery-code/reset`
- 幂等字段：`N/A`
- 鉴权要求：`DeviceAuth + WriterDeviceOnly`
- 限流策略：`RECOVERY_VERIFY`

请求 JSON 示例：

```json
{
  "old_recovery_code": "AB12CD34EF56GH78",
  "force_reset": true
}
```

响应 JSON 示例：

```json
{
  "recovery_code": "K9PQ41MS77TX8N2D",
  "recovery_code_masked": "K9PQ********8N2D",
  "shown_once": true,
  "updated_at": "2026-02-12T10:34:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `RECOVERY_CODE_INVALID`
- `STALE_WRITER_REJECTED`

### 2.9 Web 只读绑定创建

- Method & Path：`POST /api/v1/web/read-bindings`
- 幂等字段：`N/A`
- 鉴权要求：`Anonymous`
- 限流策略：`WEB_PAIR_BIND`

请求 JSON 示例：

```json
{
  "pairing_code": "24069175",
  "client_fingerprint": "9cfce7bcd5d6dfac2697fdf1f5b9f226",
  "web_device_name": "Chrome@Mac"
}
```

响应 JSON 示例：

```json
{
  "binding_id": "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
  "binding_token": "wrb_eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "pairing_code_version": 4,
  "status": "ACTIVE",
  "created_at": "2026-02-12T10:35:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `PAIRING_CODE_FORMAT_INVALID`
- `PAIRING_CODE_INVALID`
- `WEB_BINDING_VERSION_MISMATCH`

### 2.10 Web 只读绑定鉴权

- Method & Path：`POST /api/v1/web/read-bindings/auth`
- 幂等字段：`N/A`
- 鉴权要求：`WebBindingToken`
- 限流策略：`WEB_PAIR_BIND`

请求 JSON 示例：

```json
{
  "binding_token": "wrb_eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "client_fingerprint": "9cfce7bcd5d6dfac2697fdf1f5b9f226"
}
```

响应 JSON 示例：

```json
{
  "binding_id": "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
  "user_id": "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
  "status": "ACTIVE",
  "pairing_code_version": 4,
  "last_seen_at": "2026-02-12T10:36:00Z"
}
```

错误码列表：

- `RATE_LIMIT_BLOCKED`
- `UNAUTHORIZED_WEB_TOKEN`
- `WEB_BINDING_REACTIVATE_DENIED`
- `WEB_BINDING_VERSION_MISMATCH`

---

## 附录 A：FR -> 接口覆盖映射表

| FR | 需求摘要 | 覆盖接口 |
|---|---|---|
| FR-008 | 缺 START 禁止 END | `2.1 同步上报` |
| FR-016 | 打卡/请假变更自动同步并支持手动同步 | `2.1 同步上报` |
| FR-021 | 配对码生成用于 Web 绑定 | `2.5 配对码查询（含首次生成）`、`2.9 Web 只读绑定创建` |
| FR-022 | 配对码重置后旧码失效 | `2.6 配对码重置`、`2.9 Web 只读绑定创建` |
| FR-023 | 普通换机迁移（旧机确认） | `2.2 迁移申请`、`2.3 迁移确认` |
| FR-026 | 恢复码生成 | `2.7 恢复码首次生成` |
| FR-027 | 恢复码重置后旧码失效且新码仅展示一次 | `2.8 恢复码重置` |
| FR-028 | 强制接管（配对码+恢复码） | `2.4 强制接管` |
| FR-029 | 删除打卡后重算并同步 | `2.1 同步上报`（载荷含删除态与重算结果） |
| FR-031 | FULL_DAY 与 AUTO 打卡不可共存 | `2.1 同步上报` |
| FR-032 | 分钟粒度存储与一致性校验 | `2.1 同步上报` |
| FR-033 | MonthSummary 由移动端同步，Web 仅读取展示 | `2.1 同步上报`、`2.10 Web 只读绑定鉴权`（读取入口鉴权） |

---

## 附录 B：数据库拒绝原因 -> API 错误码映射（含 REJECTED/NOOP 边界）

| 分类 | 数据库规则标识 | API 返回 | 语义边界 |
|---|---|---|---|
| REJECTED | `P0001 + PUNCH_END_REQUIRES_START` | `PUNCH_END_REQUIRES_START` | 业务拒绝 |
| REJECTED | `P0001 + PUNCH_END_NOT_AFTER_START` | `PUNCH_END_NOT_AFTER_START` | 业务拒绝 |
| REJECTED | `P0001 + AUTO_PUNCH_ON_FULL_DAY_LEAVE` | `CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE` | 业务拒绝（FR-031，同一规则族 `FULL_DAY_AUTO_PUNCH_CONFLICT` 的双向触发） |
| REJECTED | `P0001 + FULL_DAY_LEAVE_WITH_AUTO_PUNCH` | `CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE` | 业务拒绝（FR-031，同一规则族 `FULL_DAY_AUTO_PUNCH_CONFLICT` 的双向触发） |
| REJECTED | `P0001 + SYNC_COMMIT_STALE_WRITER` | `STALE_WRITER_REJECTED` | 业务拒绝 |
| REJECTED | `P0001 + SYNC_COMMIT_USER_NOT_FOUND` | `USER_NOT_FOUND` | 业务拒绝 |
| REJECTED | `P0001 + MIGRATION_SOURCE_MISMATCH` | `MIGRATION_SOURCE_MISMATCH` | 业务拒绝 |
| REJECTED | `P0001 + MIGRATION_TRANSITION_INVALID` | `MIGRATION_STATE_INVALID` | 业务拒绝 |
| REJECTED | `P0001 + MIGRATION_IMMUTABLE_FIELDS` | `MIGRATION_IMMUTABLE_FIELDS` | 业务拒绝 |
| REJECTED | `P0001 + MIGRATION_USER_NOT_FOUND` | `USER_NOT_FOUND` | 业务拒绝 |
| REJECTED | `P0001 + WEB_BINDING_REACTIVATE_DENIED` | `WEB_BINDING_REACTIVATE_DENIED` | 业务拒绝（撤销不可逆） |
| REJECTED | `P0001 + WEB_BINDING_VERSION_MISMATCH` | `WEB_BINDING_VERSION_MISMATCH` | 业务拒绝 |
| REJECTED | `P0001 + WEB_BINDING_VERSION_IMMUTABLE` | `WEB_BINDING_VERSION_IMMUTABLE` | 业务拒绝 |
| REJECTED | `P0001 + WEB_BINDING_USER_ID_IMMUTABLE` | `WEB_BINDING_USER_IMMUTABLE` | 业务拒绝 |
| REJECTED | `P0001 + WEB_BINDING_USER_NOT_FOUND` | `USER_NOT_FOUND` | 业务拒绝 |
| REJECTED | `P0001 + RECORD_USER_ID_IMMUTABLE` | `RECORD_USER_IMMUTABLE` | 业务拒绝 |
| REJECTED | `P0001 + ROTATE_PAIRING_USER_NOT_FOUND` | `USER_NOT_FOUND` | 业务拒绝 |
| REJECTED | `P0001 + SECURITY_SCENE_UNSUPPORTED` | `INTERNAL_RULE_UNSUPPORTED` | 内部配置错误，拒绝 |
| REJECTED | 规则 `RULE_PAIRING_CODE_LOOKUP_MISS_AFTER_RESET`（重置后旧配对码不再命中） | `PAIRING_CODE_INVALID` | 旧配对码绑定失败 |
| REJECTED | 规则 `RULE_RECOVERY_CODE_HASH_MISMATCH_AFTER_RESET`（重置后旧恢复码哈希不匹配） | `RECOVERY_CODE_INVALID` | 旧恢复码验证失败 |
| REJECTED | `23505 + uq_sync_commits_user_sync` 且 payload_hash 不同 | `SYNC_ID_CONFLICT` + `gate_result=REJECTED` + `gate_reason=SYNC_ID_CONFLICT` | 幂等冲突拒绝 |
| NOOP | 门控规则 `RULE_SYNC_REPLAY_NOOP`（相同 `sync_id` + 相同 `payload_hash`） | `gate_result=NOOP` + `gate_reason=REPLAY_NOOP` | 幂等成功但不重复生效 |
| NOOP | 门控规则 `RULE_SYNC_LOW_OR_EQUAL_VERSION`（低/同版本 + 新 `sync_id`） | `gate_result=NOOP` + `gate_reason=LOW_OR_EQUAL_VERSION` | 丢弃旧数据但记账 |
| REJECTED | 门控规则 `RULE_SYNC_ID_CONFLICT`（相同 `sync_id` + 不同 `payload_hash`） | `gate_result=REJECTED` + `gate_reason=SYNC_ID_CONFLICT` | 幂等冲突拒绝 |
| APPLIED | 门控规则 `RULE_SYNC_APPLIED_WRITE`（写入生效） | `gate_result=APPLIED` + `gate_reason=APPLIED_WRITE` | 写入成功 |
| REJECTED | `23514 + ck_punch_records_at_utc_minute_precision` | `TIME_PRECISION_INVALID` | 分钟精度拒绝 |
| REJECTED | `23514 + ck_punch_records_local_date_match_timezone` | `TIME_FIELDS_MISMATCH` | 字段一致性拒绝 |
| REJECTED | `23514 + ck_punch_records_minute_of_day_match_at_utc` | `TIME_FIELDS_MISMATCH` | 字段一致性拒绝 |
| REJECTED | `23514 + ck_users_pairing_code_format` | `PAIRING_CODE_FORMAT_INVALID` | 参数格式拒绝 |
| REJECTED | `23505 + uk_migration_user_pending` | `MIGRATION_PENDING_EXISTS` | 状态冲突拒绝 |
| REJECTED | 限流门控：`security_attempt_windows.blocked_until > now()` | `RATE_LIMIT_BLOCKED` | 封禁窗口拒绝 |

FR-031 规则族统一说明：`AUTO_PUNCH_ON_FULL_DAY_LEAVE` 与 `FULL_DAY_LEAVE_WITH_AUTO_PUNCH` 属于同一规则族 `FULL_DAY_AUTO_PUNCH_CONFLICT` 的双向触发，API 统一映射为 `CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE`，不区分触发方向。

---

## 附录 C：门控结果字段规范（闭集枚举）

`gate_result`（闭集）：

- `APPLIED`：本次请求至少 1 项写入生效
- `NOOP`：请求被识别为安全重放或低/同版本丢弃，不产生业务副作用
- `REJECTED`：请求被拒绝

`gate_reason`（闭集）：

- `APPLIED_WRITE` -> 规则 `RULE_SYNC_APPLIED_WRITE`
- `REPLAY_NOOP` -> 规则 `RULE_SYNC_REPLAY_NOOP`
- `SYNC_ID_CONFLICT` -> 规则 `RULE_SYNC_ID_CONFLICT`
- `LOW_OR_EQUAL_VERSION` -> 规则 `RULE_SYNC_LOW_OR_EQUAL_VERSION`

约束：

- `gate_result/gate_reason` 仅出现在涉及幂等/版本门控的接口（本草案为 `2.1 同步上报`）
- `gate_reason` 必须一值一规则，禁止新增未映射值

---

## 附录 D：门控返回矩阵（固定）

| 条件 | gate_result | gate_reason | HTTP |
|---|---|---|---|
| 相同 `sync_id` + 相同 `payload_hash` | `NOOP` | `REPLAY_NOOP` | `200` |
| 相同 `sync_id` + 不同 `payload_hash` | `REJECTED` | `SYNC_ID_CONFLICT` | `409` |
| 低/同版本 + 新 `sync_id` | `NOOP` | `LOW_OR_EQUAL_VERSION` | `200` |
| 写入生效 | `APPLIED` | `APPLIED_WRITE` | `200` |

---

## 附录 E：`sync_commits.status` 映射

仅当“新增 `sync_commits` 记录”时：

- `gate_reason=APPLIED_WRITE` -> `sync_commits.status='APPLIED'`
- `gate_reason=LOW_OR_EQUAL_VERSION` -> `sync_commits.status='APPLIED'`

不新增 `sync_commits`：

- `gate_reason=REPLAY_NOOP`（复用既有 `sync_id` 记录）
- `gate_reason=SYNC_ID_CONFLICT`（复用既有 `sync_id` 记录）

---

## 附录 F：`payload_hash` 生成口径（固定）

- 算法：`SHA-256`
- 输出：小写十六进制
- 输入：UTF-8 编码的“规范化 JSON”（无额外空白）

规范化步骤：

1. 顶层键顺序固定为：  
   `user_id` / `device_id` / `writer_epoch` / `sync_id` / `punch_records` / `leave_records` / `day_summaries` / `month_summaries`
2. 对象字段顺序按本契约定义顺序输出（不按客户端原始顺序）
3. 数组排序按稳定业务键：  
   `punch_records`/`leave_records` 按 `id`；`day_summaries` 按 `local_date`；`month_summaries` 按 `month_start`
4. 时间字段统一为 UTC ISO8601 分钟粒度：`YYYY-MM-DDTHH:mm:00Z`
5. 字段白名单：仅参与写入结果的字段参与哈希（不含 `trace_id`、`client_time` 等调试/本地元数据）
6. 未知字段拒绝：请求出现白名单外字段，直接返回 `UNKNOWN_FIELD`，禁止“透传后忽略”

一致性要求：

- 语义相同（仅字段顺序/空白不同）=> `payload_hash` 必须相同
- 语义不同（业务字段值变化）=> `payload_hash` 必须不同

---

## 附录 G：时间精度与一致性规则

- API 入库前执行分钟向下取整（截断秒/毫秒）
- 取整后校验：
  - `local_date == (at_utc AT TIME ZONE timezone_id)::date`
  - `minute_of_day == hour(at_utc@timezone_id) * 60 + minute(at_utc@timezone_id)`
- 不一致时返回：`TIME_FIELDS_MISMATCH`
- 非分钟粒度直写数据库会触发：`TIME_PRECISION_INVALID`

---

## 附录 H：恢复码语义（固定）

- 恢复码为 16 位字母数字混合（`^[A-Z0-9]{16}$`）
- 重置后旧恢复码立即失效
- 明文仅在“首次生成/重置响应”返回一次，后续仅保留哈希校验路径
- 校验不区分大小写：请求入参先大写归一再参与哈希比较

---

## 附录 I：配对码语义（固定）

- 配对码格式固定：8 位数字（`^[0-9]{8}$`）
- 非 8 位数字请求直接返回：`PAIRING_CODE_FORMAT_INVALID`
- 配对码无过期机制；在未重置前持续有效
- 仅“配对码重置”可使旧配对码失效
