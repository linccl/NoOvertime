# NoOvertime Backend

NoOvertime 后端服务（Go + PostgreSQL），当前有效核心接口如下：

- `GET /health`
- `POST /api/v1/tokens/issue`
- `POST /api/v1/tokens/rotate`
- `POST /api/v1/sync/commits`
- `POST /api/v1/migrations/requests`
- `POST /api/v1/migrations/{migration_request_id}/confirm`
- `POST /api/v1/web/month-summaries/query`
- `POST /api/v1/web/day-summaries/query`

自 2026-03-14 起，以下旧流程已暂停，保留路由但统一返回 `410 FEATURE_PAUSED`：

- `POST /api/v1/migrations/takeover`
- `POST /api/v1/migrations/forced-takeover`
- `POST /api/v1/pairing-code/query`
- `POST /api/v1/pairing-code/reset`
- `POST /api/v1/recovery-code/generate`
- `POST /api/v1/recovery-code/reset`
- `POST /api/v1/web/read-bindings`
- `POST /api/v1/web/read-bindings/auth`

## 本地运行

### 1. 前置条件

- Go `1.22+`
- PostgreSQL `18.1`（或兼容版本）

### 2. 初始化数据库

```bash
createdb no_overtime
for f in db/migrations/*.sql; do
  psql -d no_overtime -f "$f"
done
```

### 3. 配置环境变量

最小必需配置：

```bash
export DATABASE_DSN='postgres://<user>:<password>@localhost:5432/no_overtime?sslmode=disable'
```

常用配置（含默认值）：

| 环境变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `DATABASE_DSN` | 是 | 无 | PostgreSQL 连接串 |
| `HTTP_ADDR` | 否 | `:29082` | 服务监听地址，格式 `host:port` |
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

### 5. Web 看板（静态页）

启动后访问：

- `http://127.0.0.1:29082/web/`

## 接口快速示例

### 健康检查

```bash
curl -i http://127.0.0.1:29082/health
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

> `payload_hash` 需为规范化请求体（不含 `payload_hash` 字段本身）的 SHA-256（64 位小写十六进制）。

```bash
curl -i -X POST 'http://127.0.0.1:29082/api/v1/sync/commits' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <mobile_token>' \
  -H 'X-Request-ID: req-sync-demo-001' \
  -d '{
    "sync_id":"bb5166cb-13ed-47a0-9fb5-58e2062a3559",
    "payload_hash":"<computed_sha256_hex>",
    "punch_records":[{"id":"4acb45c8-65cb-4e20-9602-2ac3609d5c28","local_date":"2026-02-12","type":"START","at_utc":"2026-02-12T01:10:00Z","timezone_id":"Asia/Shanghai","minute_of_day":550,"source":"MANUAL","deleted_at":null,"version":3}],
    "leave_records":[{"id":"1fc35956-0015-4aa7-a0aa-3ef6576fc423","local_date":"2026-02-11","leave_type":"AM","deleted_at":null,"version":2}],
    "day_summaries":[{"id":"3cf42a4f-8107-49dd-96bd-1cd7ea6f3f54","local_date":"2026-02-12","start_at_utc":"2026-02-12T01:10:00Z","end_at_utc":"2026-02-12T10:20:00Z","is_leave_day":false,"leave_type":null,"is_late":true,"work_minutes":550,"adjust_minutes":0,"status":"COMPUTED","version":5,"updated_at":"2026-02-12T10:21:00Z"}],
    "month_summaries":[{"id":"445f1f36-cf1c-4f90-9fd0-b56438e2df2e","month_start":"2026-02-01","work_minutes_total":6120,"adjust_minutes_balance":120,"version":5,"updated_at":"2026-02-12T10:21:00Z"}]
  }'
```

更多接口语义、错误码与 gate 字段说明见：`docs/API使用说明.md`。若要查看安卓端最新 token-only 改造对 API 端的输入，请优先阅读 `docs/安卓端TokenOnly改造对API端总览.md`。首次成功同步后，服务端会在响应中回填 `user_id`。

### Web 只读查询（token-only）

自 2026-03-14 起，Web 月/日汇总直接使用移动端 token 查询，不再接受 `binding_token`。

按年查询月汇总：

```bash
curl -i -X POST 'http://127.0.0.1:29082/api/v1/web/month-summaries/query' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <mobile_token>' \
  -H 'X-Request-ID: req-web-month-001' \
  -d '{
    "year": 2026
  }'
```

按月查询日汇总：

```bash
curl -i -X POST 'http://127.0.0.1:29082/api/v1/web/day-summaries/query' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <mobile_token>' \
  -H 'X-Request-ID: req-web-day-001' \
  -d '{
    "month_start": "2026-02-01"
  }'
```

## gate 字段语义

- `gate_result=APPLIED`: 本次写入生效
- `gate_result=NOOP`: 安全重放或低/同版本丢弃
- `gate_result=REJECTED`: 请求被拒绝

`gate_reason` 当前闭集：

- `APPLIED_WRITE`
- `REPLAY_NOOP`
- `SYNC_ID_CONFLICT`
- `LOW_OR_EQUAL_VERSION`

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
