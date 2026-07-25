# Snapshot FastForward / MsgSnapStatus 第四阶段实验（2026-07-22）

## 目标

本阶段补齐两个此前只有受控 trace 或尚未进入模型的边界：

1. 用真实 etcd-raft 消息乱序自然触发 `FastForwardSnapshot`；
2. 把 snapshot 传输成功/失败通过 `RawNode.ReportSnapshot` 反馈为
   `MsgSnapStatus`，并验证失败后的重试恢复。

动态 `ConfState` 不在本阶段实现，仍保持固定 voter。

## 自然 FastForward 场景

`snapshot-fast-forward` 在线策略不修改 Raft 内部字段：

1. 让目标 Follower 确认 Leader no-op，进入正常复制状态；
2. Leader 连续发送多个 optimistic `MsgApp`；
3. 复制其中一个消息，先乱序投递一份，保留其 reject response；
4. 再按顺序投递其余消息，使目标已经保存 snapshot index/term 对应的 entry，
   但不向它传播新的 commit；
5. 另一多数派提交并压缩相同日志；
6. 最后向 Leader 投递旧 reject response，使 `nextIndex` 回到 Storage
   `firstIndex` 之前并自然产生 `MsgSnap`；
7. 目标收到 snapshot 后命中 etcd-raft `matchTerm` 分支，只 fast-forward
   commit，不执行 Restore。

这条路径不使用内部状态注入。策略在发送 snapshot 前会丢弃目标先前成功的旧
`MsgAppResp`，避免 snapshot 成功后旧响应再次把 probe 进度回退并产生无关的第二次发送。

## MsgSnapStatus 语义

Runtime 的 Drop 现在会通知 Adapter。普通消息仍只从队列移除；对 `MsgSnap`：

- 成功投递后调用 `ReportSnapshot(SnapshotFinish)`；
- 被 Drop 时调用 `ReportSnapshot(SnapshotFailure)`；
- 重复、过期或原 sender 已不再等待 snapshot 的报告记录为 ignored stutter。

`storage-snapshot` 增加 `HandleSnapshotStatus(i,j,success,next)`：

- success：清除 pending，令 `next=max(match+1,pending+1)`；较旧 snapshot
  在传输期间可能遇到延迟的成功 `MsgAppResp`，使 `match` 前进到 pending 之后；
- failure：清除 pending，回退 `next=match+1`；
- failure 后等待下一轮 heartbeat 才重试属于调度节奏，在当前安全性模型中抽象为
  stutter；真实 `snapshot-failure` 策略仍完整执行 heartbeat/response/retry。

该实验还暴露并修正了基础模型的旧 response 抽象：成功响应现在不会降低
`matchIndex`，拒绝响应把 `nextIndex` 保持在 `matchIndex+1`，不再产生真实
etcd-raft 不可能出现的 `nextIndex <= matchIndex` 中间状态。

## 模型检查与 strict trace

- 单节点完整 `StorageSpec`：76 generated，48 distinct，depth 11，无错误；
- 两节点聚焦 `ProgressCheckSpec`：30 generated，9 distinct，depth 6，无错误；
- strict server 识别 30 个动作定义；
- controlled trace 覆盖 status success、status failure、retry send，以及
  FastForward 后的 success status；
- `go test`、`go vet`、相关 race test 和 strict server 集成测试均通过。

## 真实 etcd-raft + strict TLC 矩阵

每格运行 3 个 seed，共 24 条最终执行；全部 `policy_complete`，无 TLC failure，
节点使用正常多数派 quorum。

| 场景 | 节点 | retain | 成功/失败 | Actions | 模型事件 | 不同 TLC 状态 | Send/Deliver | Install/FastForward | status 成功/失败 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| fast-forward | 3 | 0 | 3/0 | 93 | 141 | 121 | 3/3 | 0/3 | 3/0 |
| fast-forward | 3 | 1 | 3/0 | 93 | 141 | 121 | 3/3 | 0/3 | 3/0 |
| fast-forward | 5 | 0 | 3/0 | 156 | 210 | 184 | 3/3 | 0/3 | 3/0 |
| fast-forward | 5 | 1 | 3/0 | 156 | 210 | 184 | 3/3 | 0/3 | 3/0 |
| failure/retry | 3 | 0 | 3/0 | 72 | 87 | 64 | 6/3 | 3/0 | 3/3 |
| failure/retry | 3 | 1 | 3/0 | 72 | 87 | 64 | 6/3 | 3/0 | 3/3 |
| failure/retry | 5 | 0 | 3/0 | 117 | 138 | 106 | 6/3 | 3/0 | 3/3 |
| failure/retry | 5 | 1 | 3/0 | 117 | 138 | 106 | 6/3 | 3/0 | 3/3 |

最终产物目录：

- `runs/snapshot-fast-forward-phase4-{3n,5n}-r{0,1}-20260722`
- `runs/snapshot-status-failure-phase4-{3n,5n}-r{0,1}-20260722`

## 结论与剩余边界

`FastForwardSnapshot` 已从 controlled-only 覆盖提升为真实 etcd-raft 自然覆盖；
snapshot 传输失败、Leader progress 回退、heartbeat 后重试和成功状态也已进入
Concrete trace 与 strict TLA+ 检查。

剩余 Snapshot 主要边界为动态 membership 的 `ConfState` 创建/安装、损坏 payload、
以及真实分块/流式传输实现；其中动态 membership 需要先引入 ConfChange Action 和
动态 quorum 模型，不能只在现有 snapshot 动作上增加一个字段。

## 随机映射回归修复（2026-07-22）

后续随机概率采样发现 seed `210088` 在 snapshot success status 上出现
`pending=6, match=7, next=8`，原 Mapper/TLA+ 错误期待 `next=7`。对照
etcd-raft `Progress.BecomeProbe()` 后修正为：

- `SnapshotFinish`：`next=max(match+1,pending+1)`；
- `SnapshotFailure`：`next=match+1`；
- pending 期间的 Next 不再错误地固定为 `pending+1`；
- snapshot pending 时只让真正推进到当前 `firstIndex` 的成功 `MsgAppResp`
  清除 pending，stale reject 保持不变；
- 旧 `leaderCommit` 不再使基础模型 commitIndex 回退；
- Leader 可以向当前 crashed/inactive 的目标生成并排队 MsgSnap，安装时仍要求
  目标 active。

回归结果：

- 原失败 seed：无 TLC 与 strict TLC 均通过；strict trace 为 100 Actions、
  126 model events、95 distinct model states；
- 1000 条连续随机 seed 的 Go Mapper 回归：1000/1000 completed；
- 覆盖原失败 seed 区间的 100 条 strict TLC 随机回归：100/100 completed，
  10560 model events、6468 distinct model states；
- `go test ./...`、`go vet ./...`、相关 race tests 和 TLC server 全部通过。
