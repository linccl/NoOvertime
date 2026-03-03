# P1-Batch3 实施计划：修复验收门禁问题

> 状态：历史临时计划稿（仅留痕）；当前门禁与 CI 口径以 `docs/回归门禁说明.md`、`Makefile` 与 `.github/workflows/regression.yml` 为准。

## 任务目标

修复验收口径中的问题，确保 make gate 可用，CI 能够验证核心路径实现。

全程留痕。

## 任务背景

该计划编写时存在以下问题：
1. 验收口径里的 make gate 不存在（Makefile 中没有 gate 目标）
2. CI 全绿当时不能证明 P0 完成（CI 只跑回归门禁，不覆盖后端核心路径实现测试）
3. 当时环境下门禁未通过（回归脚本在连通性阶段报远端 5432 网络被拒绝）

## 实现要求

### 1. 补充 Makefile gate 目标

在 Makefile 中添加 gate 目标，应该包括：
- 代码格式检查（gofmt）
- 代码静态分析（go vet）
- 单元测试（go test）
- 可选：集成测试

### 2. 扩展 CI 覆盖范围

修改 .github/workflows/regression.yml，确保：
- 运行 make gate（或等效的验证命令）
- 覆盖后端核心路径的单元测试
- 不仅仅是回归门禁

### 3. 修复回归门禁网络问题

检查并修复回归脚本中的网络连接问题：
- 检查 PostgreSQL 连接配置
- 确保测试环境可以正确连接数据库
- 如果是 CI 环境问题，调整配置或跳过该检查

## 验收标准

1. make gate 命令可以成功执行
2. make gate 包含代码检查和测试
3. CI 能够运行并验证核心路径实现
4. 回归门禁在本地或 CI 环境下可以通过

## 参考文档

- Makefile
- .github/workflows/regression.yml
- docs/回归门禁说明.md
- docs/回归门禁口径与落地清单.md
