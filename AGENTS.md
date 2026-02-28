<!-- SWARM-CONTEXT-START (auto-generated, do not edit) -->
# Swarm 蜂群协作上下文 (自动生成，勿手动编辑)

## 你的身份
通过环境变量确认: echo $SWARM_ROLE

## 项目信息
项目已扫描，关键配置文件: go.mod, Makefile
详情: cat /Users/linshiyu/lincc/lincc-project/swarmesh/runtime/project-info.json
请自行分析技术栈，发布任务时用 --verify 指定质量门验证命令。

## 并行开发模式
每个角色在独立的 git worktree 中工作，拥有独立分支。
你的代码修改不会与其他角色冲突。完成后由人类决定合并。

## 当前团队成员
- frontend (fe,front,frontend,0) [branch: swarm/frontend]
- backend (be,back,backend,1) [branch: swarm/backend]
- reviewer (review,rv,reviewer,2) [branch: swarm/reviewer]
- supervisor (sup,supervisor,3) [branch: swarm/supervisor]
- inspector (insp,inspector,4) [branch: swarm/inspector]


注意: 只与上述团队成员沟通。如果需要的角色不在团队中，自行承担该职责。
执行 swarm-msg.sh list-roles 可查看最新在线角色。

## 协作通讯工具

你在一个多角色蜂群中工作。使用以下 shell 命令与其他角色沟通：

### 消息（点对点）
| 命令 | 说明 |
|------|------|
| swarm-msg.sh send <role> "msg" | 发消息给指定角色 |
| swarm-msg.sh reply <id> "msg" | 回复消息 |
| swarm-msg.sh read | 查看收件箱 |
| swarm-msg.sh wait --timeout 60 | 等待新消息 |
| swarm-msg.sh list-roles | 查看在线角色 |
| swarm-msg.sh broadcast "msg" | 广播给所有人 |

### 任务队列（中心队列，任何角色可认领）
| 命令 | 说明 |
|------|------|
| swarm-msg.sh create-group "title" | 创建任务组（返回 group-id） |
| swarm-msg.sh publish <type> "title" [-g group-id] [--depends id1,id2] | 发布任务 |
| swarm-msg.sh list-tasks | 查看待认领任务 |
| swarm-msg.sh claim <task-id> | 认领任务 |
| swarm-msg.sh complete-task <id> "result" | 完成任务并反馈 |
| swarm-msg.sh group-status [group-id] | 查看任务组进度 |

任务组示例（带依赖）:
  G=$(swarm-msg.sh create-group "用户注册系统")
  T1=$(swarm-msg.sh publish develop "实现 API" -g $G)
  T2=$(swarm-msg.sh publish develop "设计数据库" -g $G)
  T3=$(swarm-msg.sh publish review "审核代码" -g $G --depends $T1,$T2)

### 行为准则
1. 当任务涉及其他角色的职责时，主动用 swarm-msg.sh send 联系对方
2. 批量任务用 create-group 创建组，用 --depends 设置依赖顺序
3. 开发完成后，代码会自动 commit 到你的分支，然后用 publish 发布审核任务
4. 审核角色从队列 claim 任务，用 git diff 审核分支代码
5. 任务完成后用 complete-task 反馈，依赖此任务的阻塞任务会自动解锁
6. 任务组全部完成时，发布者会自动收到通知
<!-- SWARM-CONTEXT-END -->
