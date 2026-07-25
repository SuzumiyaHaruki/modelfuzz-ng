# Snapshot Install TLA+ 第三阶段（2026-07-22）

## 范围

第三阶段把 `MsgSnap` 的 Follower 结果从 stutter 提升为三个明确动作：

- `InstallSnapshot`：目标没有 snapshot index/term 对应 entry，执行 Restore；
- `FastForwardSnapshot`：目标已有对应 entry，只推进 commit，随后正常 apply；
- `RejectSnapshot`：旧 term、snapshot 不领先于 commit，或同 term 发给当前 Leader。

Snapshot payload 用发送节点的 ghost committed-log prefix 表示。成功 Restore 会原子地：

```text
log[target]          := source log prefix through snapshot index
commitIndex[target]  := snapshot index
appliedIndex[target] := snapshot index
snapshotIndex/Term   := payload index/term
firstIndex[target]   := snapshot index + 1
state[target]        := Follower
```

`FastForwardSnapshot` 保留日志和 Storage snapshot 边界，只推进 commit；Ready 中自然
产生的 committed entries继续映射为 `ApplyCommitted`。`RejectSnapshot` 保持日志与
Storage 不变，但保留高 term 降级和 Candidate 收到同 term `MsgSnap` 时的降级语义。

Adapter 的 `raft.message_delivered` 现在携带 `snapshot_index/snapshot_term`，每次
`MsgSnap` 必须记录且只能记录一种 applied、fast-forwarded 或 rejected/stale 结果；
缺失、冲突或 metadata 不一致会被 Mapper 拒绝。Follower 生成的 `MsgAppResp` 仍走
真实受控网络，投递到 Leader 后由 `StorageHandleAppendEntriesResponse` 更新
`matchIndex/nextIndex` 并在 `mIndex >= pendingSnapshot` 时清除 pending。

定向 `snapshot-partition` 策略也改为先排空 lagger -> Leader 的响应，再报告
`policy_complete`，因此真实实验覆盖了 pending 清除，而不只覆盖 Follower Apply。

## 模型和 strict TLC 检查

- 单节点完整 `StorageSpec`：76 generated，48 distinct，depth 11，无错误；
- 两节点聚焦 `ProgressCheckSpec`：16 generated，6 distinct，depth 6，无错误；
- controlled trace 覆盖 Send -> Install -> duplicate Reject -> response；
- 独立 controlled trace 覆盖已有 matching entry 的 FastForward -> Apply -> response；
- 提前 Install 和提前 Send 都返回 `disabled_action`；
- strict server 识别29个动作定义。

TLC 可能通过不同表达式分支生成内容完全相同的重复 successor。strict server 现在先
执行 callable、deep-normalize，再按完整状态去重；只有归一化后仍有多个不同状态才
报告 `ambiguous_successor`。原有真正多后继回归仍通过。

## 真实 etcd-raft + strict TLC 矩阵

正常多数派、`snapshot_threshold=2`，3/5节点、retain=0/1，每种组合3个 seed：

| 节点 | retain | 成功/失败 | Actions | 模型事件 | 不同 TLC 状态 | Send/Install/Reject | TLC 失败 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 3 | 0 | 3/0 | 69 | 81 | 61 | 3/3/3 | 0 |
| 3 | 1 | 3/0 | 69 | 81 | 61 | 3/3/3 | 0 |
| 5 | 0 | 3/0 | 114 | 132 | 103 | 3/3/3 | 0 |
| 5 | 1 | 3/0 | 114 | 132 | 103 | 3/3/3 | 0 |

12次运行全部 `policy_complete`、`failure.json=null`，每条轨迹包含一次自然 Install、
一次重复 snapshot Reject，以及两个实际 `MsgAppResp` 投递。相对第二阶段，每条轨迹
增加2个 Follower outcome 模型动作和2个 response 模型动作。

原始产物位于忽略目录：

- `runs/snapshot-install-phase3-complete-3n-r0-20260722`
- `runs/snapshot-install-phase3-complete-3n-r1-20260722`
- `runs/snapshot-install-phase3-complete-5n-r0-20260722`
- `runs/snapshot-install-phase3-complete-5n-r1-20260722`

## 当前边界

固定 voter、有效 snapshot payload 下的创建、压缩、NeedSnapshot、发送、Follower
Restore/fast-forward/reject、响应和 Leader pending 清除已经进入同一 TLA+ 轨迹。
尚未建模的主要部分是 ConfState 动态成员恢复、损坏 payload、网络层
`MsgSnapStatus` failure/report，以及 snapshot chunking；因此仍不应表述成任意配置下
完整 InstallSnapshot 协议证明。
