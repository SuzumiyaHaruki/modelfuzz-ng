# Raft节点生命周期与恢复实验（2026-07-21）

## 范围

本实验只验证本地内存 etcd-raft 集群的容错状态机：Runtime 控制节点生命周期和
内存消息队列，Go Mapper 产生模型事件，controlled TLC 运行在 `127.0.0.1`。
实验不连接任何外部系统。

原 ModelFuzz 使用 `currentActive`、`Remove` 和 `Add` 表达活动节点变化。NG 保留
这一受控事件协议以兼容 `RaftActionMapper`，但将恢复成员和重置 Raft 易失状态
合并为一个 `AddToActive` 动作。稳定的 term、vote、log 和 commit 在恢复前后保留。

## 模型检查

SANY 完成 `models/raft/raft.tla` 的语法和语义分析。随后使用 `raft.cfg` 进行
200 条随机模拟，每条长度 100，共检查 20,001 个状态，没有类型或不变量错误。

## 确定性场景

下列 Plan 均依次通过真实 etcd-raft、Raft Mapper、在线 Oracle 和本机
controlled TLC：

| Plan | Concrete Action | Effect | 模型事件 | TLC状态 | Oracle Finding |
|---|---:|---:|---:|---:|---:|
| `follower-crash-restart.json` | 10 | 19 | 13 | 12 | 0 |
| `leader-crash-reelection.json` | 13 | 27 | 18 | 17 | 0 |
| `uncommitted-log-restart.json` | 17 | 32 | 21 | 20 | 0 |
| `committed-log-restart.json` | 11 | 22 | 15 | 14 | 0 |
| `repeated-crash-restart.json` | 3 | 0 | 2 | 3 | 0 |

覆盖的状态包括：

- follower停止期间消息积压，恢复后处理延迟消息并追赶日志；
- leader停止后剩余节点在更高term重新选举，旧leader恢复为follower；
- 未提交日志跨恢复保留，在新term复制并提交到索引3；
- 已提交日志、commit和累积前缀摘要跨恢复保持不变；
- 重复停止和重复恢复分别记录为`skipped`，不会重复修改节点epoch。

五条 Concrete Trace 随后执行严格重放，分别匹配 `10/10`、`13/13`、`17/17`、
`11/11` 和 `3/3` 个步骤，合计 `54/54`。

## 在线随机策略回归

命令使用20条纯初始随机轨迹，每条最多50个 PlanAction，避免把离线Mutation的
输入质量混入本轮策略验证：

```bash
go run ./cmd/modelfuzz-ng experiment \
  -config examples/config.json \
  -tlc http://127.0.0.1:2023 \
  -output runs/random-lifecycle \
  -runs 20 -initial-population 20 \
  -max-plan-actions 50 -parallelism 1 -seed 9300
```

结果为 `20/20` 完成、1,000 个 Concrete Action、901 个模型事件、539 个唯一
模型状态。20条轨迹包含节点停止动作，19条包含恢复动作，没有 Mapper、Runtime、
模型或 Oracle 失败。

随机策略只对运行节点生成停止候选、只对已停止节点生成恢复候选，默认同时最多
停止1个节点，并始终保留至少1个运行节点。发往停止节点的消息保留在 Runtime
队列中，但不会成为投递候选；恢复后可以继续投递。

## 自动回归

`cmd/modelfuzz-ng/main_test.go` 会逐一运行上述五条 Plan，检查最终角色、epoch、
commit、last index、`Remove/Add` 模型事件和 Oracle 结果，并严格重放全部 Trace。
TLA+ 的 SANY、随机模拟和真实 controlled TLC 仍作为显式集成实验运行。
