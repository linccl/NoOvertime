# mobile_tokens 兼容迁移说明

## 背景

2026-03-15 联机回归时发现：

- `POST /api/v1/tokens/issue` 返回 `500 INTERNAL_ERROR`
- `POST /api/v1/tokens/rotate` 返回 `500 INTERNAL_ERROR`
- `GET /health` 正常

这说明服务进程可用，但 token 主链路在数据库访问阶段失败。

## 根因

`mobile_tokens` 是在 2026-03-08 的 token-only 改造里补进 `db/migrations/001_init.sql` 的，但仓库当时没有追加增量迁移文件。

对已经在更早时间执行过旧版 `001_init.sql` 的库：

- `001_init.sql` 不会自动重跑
- `mobile_tokens` 表不会自动出现
- token issue / rotate 会在访问 `mobile_tokens` 时返回 500

本次实际核对结果：

- `no_overtime_task04`：缺表，已补
- `no_overtime`：缺表，且线上 API 实际使用的是这一库

## 修复内容

新增兼容迁移：

- `db/migrations/002_mobile_tokens_compat.sql`

作用：

- 为老库补建 `mobile_tokens`
- 补建 `uq_mobile_tokens_active_user`
- 对新空库保持兼容；若 `001_init.sql` 已经建表，`002` 为 no-op

同时同步调整：

- `README.md`：本地初始化改为顺序执行 `db/migrations/*.sql`
- `scripts/pg18_regression.sh`：回归脚本改为顺序执行迁移目录，并把 `mobile_tokens` 纳入对象清单

## 执行记录

已执行远端兼容迁移：

```bash
psql "host=45.207.209.114 port=5432 dbname=no_overtime_task04 user=user_2me8xA sslmode=prefer" \
  -X -v ON_ERROR_STOP=1 \
  -f db/migrations/002_mobile_tokens_compat.sql

psql "host=45.207.209.114 port=5432 dbname=no_overtime user=user_2me8xA sslmode=prefer" \
  -X -v ON_ERROR_STOP=1 \
  -f db/migrations/002_mobile_tokens_compat.sql
```

执行结果：

- 两个库均返回 `BEGIN / CREATE TABLE / CREATE INDEX / COMMIT`

## 验证结果

库结构验证：

- `no_overtime.mobile_tokens` 已存在
- `no_overtime_task04.mobile_tokens` 已存在

接口验证：

```text
POST /api/v1/tokens/issue -> 200
POST /api/v1/tokens/rotate -> 200
```

示例响应口径：

```json
{
  "token": "tok_xxx",
  "user_id": null,
  "token_status": "ANONYMOUS"
}
```

## 剩余说明

- 本次修复的是后端 DB 兼容问题，不涉及 Android 代码改动
- Android 端通过 ADB 自动输入 token 时，当前设备上的中文输入法会影响带 `-` / `_` 的 token 录入；人工测试时应使用英文输入法，或输入后明确按回车提交
