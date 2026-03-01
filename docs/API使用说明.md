# API 使用说明

本文档用于本地联调与调用示例，当前已实现路由如下（以 `internal/api/server.go` 注册为准；其中 `/health` 与 `/api/v1/sync/commits` 提供详细示例，其余接口语义见 `docs/API契约草案.md`）：

- `GET /health`
- `POST /api/v1/sync/commits`
- `POST /api/v1/migrations/requests`
- `POST /api/v1/migrations/{migration_request_id}/confirm`
- `POST /api/v1/migrations/forced-takeover`
- `POST /api/v1/pairing-code/query`
- `POST /api/v1/pairing-code/reset`
- `POST /api/v1/recovery-code/generate`
- `POST /api/v1/recovery-code/reset`
- `POST /api/v1/web/read-bindings`
- `POST /api/v1/web/read-bindings/auth`
- `POST /api/v1/web/month-summaries/query`
- `POST /api/v1/web/day-summaries/query`

## 1. 通用约定

- 请求/响应默认 `Content-Type: application/json`。
- 支持传入 `X-Request-ID`，若未传入服务端会自动生成。
- 所有错误响应都包含以下结构：

```json
{
  "error_code": "INVALID_ARGUMENT",
  "message": "error message",
  "request_id": "req-xxx"
}
```

## 2. 健康检查：`GET /health`

### 请求

```bash
curl -i http://127.0.0.1:8080/health
```

### 成功响应（HTTP 200）

```json
{
  "app": {
    "status": "ok"
  },
  "database": {
    "status": "ok"
  }
}
```

### 数据库异常响应（HTTP 503）

```json
{
  "app": {
    "status": "degraded"
  },
  "database": {
    "status": "down",
    "message": "database unavailable"
  }
}
```

## 3. 同步上报：`POST /api/v1/sync/commits`

## 3.1 请求示例

> `payload_hash` 必须是规范化请求体的 SHA-256（64 位小写十六进制）。

```bash
curl -i -X POST 'http://127.0.0.1:8080/api/v1/sync/commits' \
  -H 'Content-Type: application/json' \
  -H 'X-Request-ID: req-sync-001' \
  -d '{
    "user_id":"8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
    "device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3",
    "writer_epoch":12,
    "sync_id":"bb5166cb-13ed-47a0-9fb5-58e2062a3559",
    "payload_hash":"<computed_sha256_hex>",
    "punch_records":[{"id":"4acb45c8-65cb-4e20-9602-2ac3609d5c28","local_date":"2026-02-12","type":"START","at_utc":"2026-02-12T01:10:00Z","timezone_id":"Asia/Shanghai","minute_of_day":550,"source":"MANUAL","deleted_at":null,"version":3}],
    "leave_records":[{"id":"1fc35956-0015-4aa7-a0aa-3ef6576fc423","local_date":"2026-02-11","leave_type":"AM","deleted_at":null,"version":2}],
    "day_summaries":[{"id":"3cf42a4f-8107-49dd-96bd-1cd7ea6f3f54","local_date":"2026-02-12","start_at_utc":"2026-02-12T01:10:00Z","end_at_utc":"2026-02-12T10:20:00Z","is_leave_day":false,"leave_type":null,"is_late":true,"work_minutes":550,"adjust_minutes":0,"status":"COMPUTED","version":5,"updated_at":"2026-02-12T10:21:00Z"}],
    "month_summaries":[{"id":"445f1f36-cf1c-4f90-9fd0-b56438e2df2e","month_start":"2026-02-01","work_minutes_total":6120,"adjust_minutes_balance":120,"version":5,"updated_at":"2026-02-12T10:21:00Z"}]
  }'
```

## 3.2 成功响应（写入生效）

```json
{
  "request_id": "req-sync-001",
  "gate_result": "APPLIED",
  "gate_reason": "APPLIED_WRITE",
  "sync_commit": {
    "sync_id": "bb5166cb-13ed-47a0-9fb5-58e2062a3559",
    "status": "APPLIED",
    "created_at": "2026-02-12T10:21:00Z"
  }
}
```

## 3.3 NOOP 响应示例（幂等重放）

```json
{
  "request_id": "req-sync-replay-001",
  "gate_result": "NOOP",
  "gate_reason": "REPLAY_NOOP",
  "sync_commit": {
    "sync_id": "bb5166cb-13ed-47a0-9fb5-58e2062a3559",
    "status": "APPLIED",
    "created_at": "2026-02-12T10:21:00Z"
  }
}
```

## 3.4 REJECTED 响应示例（冲突）

```json
{
  "error_code": "SYNC_ID_CONFLICT",
  "message": "same sync_id but different payload_hash",
  "gate_result": "REJECTED",
  "gate_reason": "SYNC_ID_CONFLICT",
  "request_id": "req-sync-conflict-001"
}
```

## 4. gate 字段语义

`gate_result`：

- `APPLIED`: 本次提交落库生效
- `NOOP`: 请求可安全忽略（重放或低/同版本）
- `REJECTED`: 请求被拒绝

`gate_reason`：

- `APPLIED_WRITE`
- `REPLAY_NOOP`
- `LOW_OR_EQUAL_VERSION`
- `SYNC_ID_CONFLICT`

## 5. 主要错误码语义

| error_code | 说明 |
|---|---|
| `INVALID_ARGUMENT` | 参数格式/范围/必填校验失败 |
| `UNKNOWN_FIELD` | JSON 出现未声明字段 |
| `SYNC_ID_CONFLICT` | 相同 `sync_id` 但载荷冲突 |
| `PUNCH_END_REQUIRES_START` | 缺少 `START` 却提交 `END` |
| `PUNCH_END_NOT_AFTER_START` | `END` 不晚于 `START` |
| `CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE` | 全天请假与自动打卡冲突 |
| `TIME_PRECISION_INVALID` | 时间不是分钟精度 |
| `TIME_FIELDS_MISMATCH` | `at_utc`/`timezone_id`/`local_date`/`minute_of_day` 不一致 |
| `STALE_WRITER_REJECTED` | 非当前写入端（`device_id` 或 `writer_epoch` 过期） |

补充说明：

- 低/同版本属于 `NOOP` 语义，`gate_reason=LOW_OR_EQUAL_VERSION`，不是失败错误码。
- 所有成功/失败响应均有可追踪 `request_id`。
