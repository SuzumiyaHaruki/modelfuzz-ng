# 非 Leader 请求、原因码与 Timer 实验（2026-07-21）

## 本次改动

- Follower 的客户端请求不再被 Profile 提前判为不可执行。已知 Leader 时，
  etcd-raft 产生受控网络消息 `MsgProp`；消息投递到当前 Leader 时才映射为
  TLA+ `ClientRequest`。
- Candidate、无已知 Leader 的 Follower 或关闭 proposal forwarding 时，Adapter
  把 `ErrProposalDropped` 记录为 `raft.proposal_dropped`，模型明确 stutter，执行不失败。
- Resolver 与 Raft Profile 的非成功判定增加稳定 `reason_code`。实验同时汇总
  `decision_counts` 和 `decision_counts_by_source`。
- Raft fork 只读暴露 election/heartbeat 的 elapsed、timeout 和随机 election
  timeout。Observation 进一步计算剩余 tick，Profile 用它判断 AdvanceTime 是否会
  越过 term 上界。
- 在线随机策略允许向已知当前 Leader 的 Follower 发送请求，支持调度 `MsgProp`，
  并过滤确定会被丢弃的请求、越界消息和不安全的下一 tick。

## 定向完整轨迹

Plan：`examples/plans/follower-request-forwarding.json`

结果目录：`runs/follower-request-forwarding-final-20260721`

- 状态：`completed`
- Concrete Action：12
- Effect：28
- 模型事件：15
- TLC 状态：13
- Oracle Finding：0
- 轨迹包含 1 条 `MsgProp`；模型只在该消息投递到节点 1 后产生请求值 1 的
  `ClientRequest`。
- 最终节点 1/2 的 `last_index` 均为 2，`commit` 均为 2。

## 100 次反馈闭环

结果目录：`runs/nonleader-timer-reasons-100-20260721`

配置：非 LLM、controlled TLC、单并发、每条最多 30 个 PlanAction、只保存汇总产物。

| 指标 | 结果 |
|---|---:|
| 完成/失败 | 100 / 0 |
| Concrete Action | 2657 |
| 模型事件 | 2401 |
| 唯一模型状态 | 457 |
| Corpus | 55 |
| Request | 57 |
| MsgProp | 16 |
| AdvanceTime | 181 |
| 强制 election timer | 552 |
| 自然 heartbeat timer | 28 |

稳定原因统计均来自离线变异轨迹：

- `message_not_available`: 291
- `partial_availability`: 8
- `target_not_running`: 21

100 次执行全部成功，没有 `unsupported_by_model`、模型越界终止或 Oracle 失败。
在线随机初始轨迹没有产生上述无效判定，说明状态感知候选过滤生效；离线变异仍会
保留这些探索性组合，并由稳定原因码单独归类。

## 验证命令

```bash
go test ./...
go vet ./...
go test -race ./...
```

本地 Raft fork 另行执行 `go test ./...`，timer 状态字段及 JSON 状态输出测试通过。
