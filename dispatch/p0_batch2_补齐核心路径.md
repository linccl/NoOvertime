# P0-Batch2：补齐核心写路径（migration/配对码/恢复码）

## 全程留痕要求
请在执行过程中详细记录所有操作步骤、代码变更和验证结果。

## 背景与问题

**当前状态**：
- ✅ 已完成：sync 接口（POST /api/v1/sync/commits）
- ✅ 已完成：健康检查接口（GET /health）
- ✅ 已完成：基础框架（配置、数据库、中间件、错误处理）

**缺失功能（P0 级别）**：
1. **Migration 接口缺失**：
   - POST /api/v1/migrations/requests（迁移申请）
   - POST /api/v1/migrations/{id}/confirm（迁移确认）
   - POST /api/v1/migrations/forced-takeover（强制接管）

2. **配对码接口缺失**：
   - POST /api/v1/pairing-code/query（查询/生成配对码）
   - POST /api/v1/pairing-code/reset（重置配对码）

3. **恢复码接口缺失**：
   - POST /api/v1/recovery-code/generate（生成恢复码）
   - POST /api/v1/recovery-code/reset（重置恢复码）

4. **Web 只读绑定接口缺失**：
   - POST /api/v1/web/read-bindings（创建绑定）
   - POST /api/v1/web/read-bindings/auth（鉴权）

5. **DB error_key 映射不完整**：
   - 当前只映射了 sync 相关错误码
   - 缺少 MIGRATION_*、PAIRING_*、RECOVERY_*、WEB_BINDING_* 等错误码映射

6. **验收问题**：
   - make gate 目标不存在（需要添加到 Makefile）
   - 服务启动验证未完成

## 任务目标

实现 API 契约草案中定义的所有核心写路径接口，确保：
1. 所有接口可调用
2. DB error_key 完整映射到 API 错误码
3. 服务可启动
4. make gate 可执行并通过
5. 所有测试通过

## 参考文档

- docs/API契约草案.md - 完整接口定义
  - 2.2 迁移申请（line 188）
  - 2.3 迁移确认（line 227）
  - 2.4 强制接管（line 264）
  - 2.5 配对码查询（line 302）
  - 2.6 配对码重置（line 334）
  - 2.7 恢复码生成（line 367）
  - 2.8 恢复码重置（line 399）
  - 2.9 Web 只读绑定创建（line 432）
  - 2.10 Web 只读绑定鉴权（line 468）
  - 附录 B：错误码映射（line 524）

- docs/数据库方案草案.md - 数据库设计
  - 4.1 用户与设备（line 46）
  - 4.5 迁移与接管事件（line 201）
  - 4.6 配对/恢复码安全限流（line 234）
  - 4.8 Web 只读绑定会话（line 281）
  - 7.2 配对码重置自动撤销 Web 绑定（line 563）
  - 7.3 迁移状态机触发器（line 612）

- docs/需求文档.md - 业务规则
  - FR-021 到 FR-028：配对码、恢复码、迁移相关需求

## 实现要求

### 1. 代码组织
- 保留现有代码结构
- 新增接口建议放在独立文件中（如 migrations.go、pairing.go、recovery.go、web_bindings.go）
- 复用现有的错误处理、中间件、数据库事务能力

### 2. 接口实现
每个接口需要包含：
- 路由注册
- 请求解析与验证
- 业务逻辑处理
- 数据库操作（使用事务）
- 错误码映射
- 响应组装

### 3. 错误处理
- 实现完整的 DB error_key 到 API 错误码映射
- 参考 API契约草案.md 附录 B（line 524-556）
- 确保所有数据库触发器错误都能正确映射

### 4. 测试
- 每个接口都需要单元测试
- 覆盖正常流程和错误场景
- 测试错误码映射的正确性

### 5. 验收
- 添加 make gate 目标到 Makefile
- 确保 make gate 可以执行并通过
- 提供服务启动验证步骤
- 所有测试通过（go test ./...）

## 验收标准

### P0（必须完成）
- [ ] 所有 10 个接口已实现并可调用
- [ ] DB error_key 完整映射（覆盖 migration/pairing/recovery/web_binding 相关错误）
- [ ] 所有接口有单元测试
- [ ] go test ./... 全部通过

### P1（重要）
- [ ] make gate 目标存在且可执行
- [ ] 提供服务启动验证文档
- [ ] 错误码映射有测试覆盖

### P2（建议）
- [ ] 集成测试覆盖关键流程
- [ ] API 文档更新

## 实现建议

1. **优先级**：
   - 先实现 migration 接口（最复杂）
   - 再实现 pairing/recovery 接口
   - 最后实现 web bindings 接口

2. **复用现有能力**：
   - 使用 internal/db/Client.WithTx 处理事务
   - 使用 internal/errors/APIError 处理错误
   - 使用现有中间件（request_id、日志、恢复）

3. **错误映射**：
   - 扩展现有的错误映射函数
   - 或创建统一的错误映射模块

4. **测试策略**：
   - 参考 sync_commits_test.go 的测试模式
   - 使用 fake/mock 数据库进行单元测试

## 注意事项

1. **不要破坏现有功能**：sync 接口和健康检查必须继续工作
2. **遵循现有代码风格**：保持与现有代码一致的风格
3. **完整的错误处理**：所有数据库错误都要正确映射
4. **测试覆盖**：每个接口都要有测试

## 交付物

1. 新增的接口实现代码
2. 新增的测试代码
3. 更新的 Makefile（添加 gate 目标）
4. 更新的文档（如果需要）
5. 验收报告（包含测试结果和启动验证）

---

**重要提示**：请 codex 根据自己认为的最佳方案实现，包括：
- 代码组织方式
- 实现顺序
- 测试策略
- 错误处理方式

全程留痕，记录所有关键决策和实现细节。
