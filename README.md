# NoOvertime Backend

NoOvertime 后端服务（Go + PostgreSQL），当前提供以下核心接口：

- `GET /health`
- `POST /api/v1/sync/commits`

## 本地运行

### 1. 前置条件

- Go `1.22+`
- PostgreSQL `18.1`（或兼容版本）

### 2. 初始化数据库

```bash
createdb no_overtime
psql -d no_overtime -f db/migrations/001_init.sql
```

### 3. 配置环境变量

最小必需配置：

```bash
export DATABASE_DSN='postgres://postgres:postgres@localhost:5432/no_overtime?sslmode=disable'
```

常用配置（含默认值）：

| 环境变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `DATABASE_DSN` | 是 | 无 | PostgreSQL 连接串 |
| `HTTP_ADDR` | 否 | `:8080` | 服务监听地址，格式 `host:port` |
| `LOG_LEVEL` | 否 | `info` | `debug/info/warn/error` |
| `DB_POOL_MAX_CONNS` | 否 | `10` | 连接池最大连接数 |
| `DB_POOL_MIN_CONNS` | 否 | `1` | 连接池最小连接数 |
| `DB_POOL_MAX_LIFETIME_SEC` | 否 | `3600` | 连接最大生命周期（秒） |
| `DB_POOL_MAX_IDLE_SEC` | 否 | `300` | 连接最大空闲时长（秒） |
| `CONFIG_FILE` | 否 | 无 | JSON 配置文件路径（先读文件，再由环境变量覆盖） |

### 4. 启动服务

```bash
go run ./cmd/api
```

## 接口快速示例

### 健康检查

```bash
curl -i http://127.0.0.1:8080/health
```

成功示例（HTTP 200）：

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

### 同步上报

> `payload_hash` 需为请求体规范化内容的 SHA-256（64 位小写十六进制）。

```bash
curl -i -X POST 'http://127.0.0.1:8080/api/v1/sync/commits' \
  -H 'Content-Type: application/json' \
  -H 'X-Request-ID: req-sync-demo-001' \
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

更多接口语义、错误码与 gate 字段说明见：`docs/API使用说明.md`。

## gate 字段语义

- `gate_result=APPLIED`: 本次写入生效
- `gate_result=NOOP`: 安全重放或低/同版本丢弃
- `gate_result=REJECTED`: 请求被拒绝

`gate_reason` 当前闭集：

- `APPLIED_WRITE`
- `REPLAY_NOOP`
- `LOW_OR_EQUAL_VERSION`
- `SYNC_ID_CONFLICT`

## 常见错误码

- `INVALID_ARGUMENT`
- `UNKNOWN_FIELD`
- `SYNC_ID_CONFLICT`
- `PUNCH_END_REQUIRES_START`
- `PUNCH_END_NOT_AFTER_START`
- `CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE`
- `TIME_PRECISION_INVALID`
- `TIME_FIELDS_MISMATCH`
- `STALE_WRITER_REJECTED`

所有错误响应均包含 `request_id` 便于链路追踪。
