# P0-Batch1 任务批次：后端核心链路实现

## 全程留痕要求
请在执行过程中详细记录所有操作步骤、代码变更和验证结果。

## 背景
- 项目名称：NoOvertime（工时管理系统）
- 技术栈：Go + PostgreSQL 18.1
- 已有资产：数据库迁移脚本 db/migrations/001_init.sql、API 契约草案文档
- 目标：实现最小后端服务，跑通核心写路径

## 任务列表

### 任务1：搭建 Go 后端服务基础框架

**目标**：
创建完整的 Go 项目基础架构，实现 HTTP 服务和数据库连接。

**具体要求**：
1. 创建标准 Go 项目结构：
   - cmd/api/ - 主程序入口
   - internal/api/ - HTTP 路由和处理器
   - internal/db/ - 数据库连接和操作
   - internal/models/ - 数据模型
   - internal/errors/ - 错误处理
   - pkg/ - 可复用的公共包
   - config/ - 配置管理

2. 实现 HTTP 服务器：
   - 使用标准库 net/http 或 gin 框架
   - 支持优雅关闭
   - 实现请求日志中间件
   - 实现错误恢复中间件

3. 实现数据库连接池：
   - 使用 pgx 或 database/sql + pq 驱动
   - 支持连接池配置
   - 实现健康检查
   - 支持事务管理

4. 实现统一的错误处理机制：
   - 将 DB error_key 映射到 API 错误码
   - 参考 docs/API契约草案.md 附录 B 的映射表
   - 实现标准错误响应格式：
     ```json
     {
       "error_code": "RATE_LIMIT_BLOCKED",
       "message": "too many attempts",
       "request_id": "8ea8b7b5-0f6f-43c1-ad03-6528cafc9ef1"
     }
     ```

5. 实现健康检查接口：
   - GET /health
   - 返回服务状态和数据库连接状态

6. 创建配置文件和环境变量支持：
   - 数据库连接配置
   - 服务端口配置
   - 日志级别配置

**验收标准**：
- [ ] 项目结构符合 Go 最佳实践
- [ ] 服务可以成功启动
- [ ] 健康检查接口返回 200 状态码
- [ ] 数据库连接正常
- [ ] 错误处理机制完整
- [ ] 有完整的 README 说明如何运行
- [ ] 有单元测试覆盖核心功能

**参考文档**：
- docs/API契约草案.md - 错误码定义
- docs/数据库方案草案.md - 数据库连接信息
- db/migrations/001_init.sql - 数据库 schema

---

### 任务2：实现同步上报接口（POST /api/v1/sync/commits）

**目标**：
实现完整的同步上报接口，支持打卡、请假、日汇总、月汇总的原子提交。

**具体要求**：
1. 实现接口路由和处理器：
   - POST /api/v1/sync/commits
   - 请求体解析和验证
   - 响应体生成

2. 实现幂等性控制：
   - 基于 sync_id + payload_hash 的幂等检查
   - 相同 sync_id + 相同 payload_hash：返回 NOOP（幂等成功）
   - 相同 sync_id + 不同 payload_hash：返回 REJECTED（冲突错误）
   - 实现 payload_hash 生成逻辑（SHA-256，参考 API 契约附录 F）

3. 实现版本控制：
   - 检查 version 字段
   - 高版本覆盖低版本
   - 低版本或同版本按 NOOP 处理

4. 实现写入端校验：
   - 验证 device_id == users.writer_device_id
   - 验证 writer_epoch == users.writer_epoch
   - 非当前写入端返回 STALE_WRITER_REJECTED

5. 实现业务规则校验：
   - 缺 START 禁止 END（PUNCH_END_REQUIRES_START）
   - END 必须晚于 START（PUNCH_END_NOT_AFTER_START）
   - 全天请假禁自动打卡（CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE）
   - 时间精度校验（TIME_PRECISION_INVALID）
   - 时间字段一致性校验（TIME_FIELDS_MISMATCH）

6. 实现原子事务提交：
   - 在同一事务中提交：
     - punch_records
     - leave_records
     - day_summaries
     - month_summaries
     - sync_commits
   - 事务失败时回滚所有变更

7. 实现错误码映射：
   - 将数据库 error_key 映射到 API 错误码
   - 参考 API 契约附录 B 的完整映射表
   - 实现所有必需的错误码：
     - INVALID_ARGUMENT
     - UNKNOWN_FIELD
     - SYNC_ID_CONFLICT
     - LOW_OR_EQUAL_VERSION_NOOP
     - PUNCH_END_REQUIRES_START
     - PUNCH_END_NOT_AFTER_START
     - CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE
     - TIME_PRECISION_INVALID
     - TIME_FIELDS_MISMATCH
     - STALE_WRITER_REJECTED

8. 实现门控结果字段：
   - gate_result: APPLIED / NOOP / REJECTED
   - gate_reason: APPLIED_WRITE / REPLAY_NOOP / SYNC_ID_CONFLICT / LOW_OR_EQUAL_VERSION

**验收标准**：
- [ ] 接口可以正常调用
- [ ] 幂等性测试通过（相同请求重复提交返回 NOOP）
- [ ] 冲突检测测试通过（相同 sync_id 不同 payload 返回冲突）
- [ ] 版本控制测试通过（低版本不覆盖高版本）
- [ ] 写入端校验测试通过（非写入端被拒绝）
- [ ] 业务规则测试通过（所有负例被正确拒绝）
- [ ] 原子性测试通过（部分失败时全部回滚）
- [ ] 错误码映射测试通过（所有错误码正确返回）
- [ ] 有完整的单元测试和集成测试
- [ ] 有详细的测试报告

**参考文档**：
- docs/API契约草案.md - 接口定义（2.1 同步上报）
- docs/需求文档.md - 业务规则（第 6 节）
- docs/数据库方案草案.md - 约束和触发器
- db/migrations/001_init.sql - 数据库 schema

---

## 验收要求

1. **代码质量**：
   - 符合 Go 最佳实践
   - 有清晰的代码注释
   - 有完整的错误处理
   - 有合理的日志记录

2. **测试覆盖**：
   - 所有接口都有单元测试
   - 关键业务逻辑有集成测试
   - 测试覆盖率 > 80%

3. **文档完整**：
   - README 说明如何运行
   - API 文档说明接口用法
   - 测试报告说明验收结果

4. **门禁通过**：
   - make gate 执行通过
   - 所有测试通过
   - 代码格式检查通过

## 交付物

1. 完整的 Go 项目代码
2. 单元测试和集成测试代码
3. README 和 API 文档
4. 测试报告和验收报告
5. 实施过程记录（全程留痕）
