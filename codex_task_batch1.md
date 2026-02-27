# Codex 任务批次 1：PostgreSQL 16 环境验证

## 背景

当前三项交付物（001_init.sql、API契约草案.md、数据库验证记录.md）已产出完成，但因缺少 PostgreSQL 16 环境而无法完成 DB 层实跑验证。这是当前的核心阻塞项。

## 任务目标

获取并验证可用的 PostgreSQL 16 环境，为后续 DB 实跑验证做好准备。

## 具体任务

### 任务 1：获取并验证 PostgreSQL 16 环境（全程留痕）

**执行步骤：**

1. 阅读 `docs/数据库链接信息.md` 获取数据库连接信息
2. 连接到数据库并验证 PostgreSQL 版本
   - 执行 `SHOW server_version_num;` 和 `SHOW server_version;`
   - 验证版本号必须 >= 160000 且 < 170000（即 PostgreSQL 16.x）
3. 验证 pgcrypto 扩展可用性
   - 检查扩展是否已安装
   - 如未安装，使用事务探测 SQL 验证是否可创建：
     ```sql
     BEGIN;
     CREATE EXTENSION IF NOT EXISTS pgcrypto;
     ROLLBACK;
     ```
   - 记录 extnamespace 信息
4. 验证目标库是否为空库
   - 按照 `docs/三项交付物执行计划.md` 第 4 章步骤 1 的空库判定标准执行
   - 使用文档中提供的空库判定 SQL
   - 确认非扩展归属对象计数为 0
5. 记录所有验证结果
   - 将结果更新到 `docs/数据库验证记录.md` 第 2 章"前置检查（PG16 环境）"部分
   - 替换所有"待执行（无可用 PG16 环境）"的占位符为实际执行结果
   - 记录时区信息（server TimeZone、session TimeZone）
   - 记录数据库名称和执行时间

**验收标准：**

- [ ] 成功连接到 PostgreSQL 数据库
- [ ] PostgreSQL 版本验证通过（16.x）
- [ ] pgcrypto 扩展可用性验证完成（已安装或可创建）
- [ ] 空库判定完成并记录结果
- [ ] 所有验证结果已更新到 `docs/数据库验证记录.md`
- [ ] 如果环境不满足要求，已明确记录阻塞原因和解决方案

**重要提醒：**

- 全程留痕：所有 SQL 命令、执行结果、错误信息都要详细记录
- 严格按照 `docs/三项交付物执行计划.md` 的前置检查要求执行
- 完成后自行验收，确认环境是否满足后续 DB 实跑的要求
- 如果发现任何问题或阻塞，必须明确记录并提出解决方案
