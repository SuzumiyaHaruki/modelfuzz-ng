# Storage/Snapshot TLA+ 真实端到端实验（2026-07-22）

## 目标

验证第一阶段 `storage-snapshot` profile 不仅能通过手工 controlled trace，还能接收
真实 etcd-raft 在分区、提交、快照、压缩、heal 和 snapshot recovery 中自然产生的
事件顺序。实验使用正常多数派 `vote_quorum_divisor=2`，不启用人工 quorum mutant。

## 方法

分别以三节点和五节点 controlled cfg 启动 strict TLC：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft_storage_snapshot.tla \
  --config models/raft/raft-storage-snapshot-10.cfg \
  --port 22035

tools/tlc-server/run.sh \
  --model models/raft/raft_storage_snapshot.tla \
  --config models/raft/raft-storage-snapshot-5nodes-10.cfg \
  --port 22035
```

每个矩阵运行3个在线 `snapshot-partition` seed，`snapshot_threshold=2`，分别覆盖
`retain_entries=0/1`。策略自然执行 Leader 选举、隔离 lagger、多数派提交、创建
snapshot、压缩日志、heal、发送并重复投递 `MsgSnap`，最终以
`policy_complete` 收敛。

原始产物保存在以下忽略目录：

- `runs/storage-snapshot-phase1-3n-r0-20260722`
- `runs/storage-snapshot-phase1-3n-r1-20260722`
- `runs/storage-snapshot-phase1-5n-r0-20260722`
- `runs/storage-snapshot-phase1-5n-r1-20260722`

## 结果

| 节点 | retain | seeds | 成功/失败 | Actions | 模型事件 | 不同 TLC 状态 | Apply/Create/Compact | TLC 请求失败 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 3 | 0 | 3 | 3/0 | 63 | 66 | 52 | 9/3/3 | 0 |
| 3 | 1 | 3 | 3/0 | 63 | 66 | 52 | 9/3/3 | 0 |
| 5 | 0 | 3 | 3/0 | 108 | 117 | 94 | 15/3/3 | 0 |
| 5 | 1 | 3 | 3/0 | 108 | 117 | 94 | 15/3/3 | 0 |

12次运行全部满足：

- `termination=policy_complete`；
- `failure.json=null`，没有 Runtime、Mapper、TLC 或 Oracle failure；
- snapshot created/sent/applied/stale 各12次，delivered 24次；
- `CreateSnapshot` 和 `CompactLog` 各进入 TLC 12次；
- 所有 strict TLC 请求成功，`errors_by_code` 为空。

retain 窗口的具体边界也与模型一致：

- retain=0：snapshot index=2，`Compact(2)`，最终 `firstIndex=3`，每次压缩2条；
- retain=1：snapshot index=2，`Compact(1)`，最终 `firstIndex=2`，每次压缩1条。

三节点每条轨迹为21个 Concrete Action、22个模型事件、23个返回模型状态；五节点
每条为36个 Concrete Action、39个模型事件、40个返回模型状态。五节点产生更多
`ApplyCommitted`（每组15次而非9次），证明多数派复制和应用路径确实进入扩展模型，
不是只重放同一条三节点轨迹。

## 一条代表性自然序列

三节点 retain=0 的 seed 470721 在 Leader 收到 `MsgAppResp` 后，同一 Concrete
Action 自然产生：

```text
raft.entry_committed(index=2)
raft.snapshot_created(index=2, term=1)
raft.log_compacted(compact_index=2)
```

Go Mapper 按依赖顺序提交给 TLC：

```text
AdvanceCommitIndex
ApplyCommitted(index=2)
CreateSnapshot(index=2, term=1)
CompactLog(index=2)
```

之后 lagger 安装 index=2 snapshot，Concrete Observation 达到
`last=commit=applied=snapshot=2, firstIndex=3`；重复副本被记录为 stale。

## 结论与边界

第一阶段模型已经覆盖真实 etcd-raft 的本地 Apply/CreateSnapshot/CompactLog 边界，
并在3/5节点和两种 retain 窗口下与 strict TLC 一致。该结果不能扩展解释为
InstallSnapshot 已形式化：`MsgSnap` 发送、Follower 安装和 stale 拒绝仍由 Concrete
Trace/Oracle 检查并在 TLA+ 中 stutter。下一阶段应针对 `NeedSnapshot`、
`SnapshotAvailable` 和 Leader progress 建模，而不是直接把当前12次成功当作完整
快照协议证明。
