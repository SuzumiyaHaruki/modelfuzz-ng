# Snapshot Leader Progress TLA+ 第二阶段（2026-07-22）

## 范围

第二阶段在第一阶段的 `appliedIndex`、`snapshotIndex`、`snapshotTerm` 和
`firstIndex` 边界之上，复用基础 Raft 模型的 `nextIndex`/`matchIndex`，新增
`pendingSnapshot[leader][peer]`，并定义：

```text
EntryAvailable(i,j)     == firstIndex[i] <= nextIndex[i][j] <= Len(log[i])+1
NeedSnapshot(i,j)       == Leader 且 nextIndex[i][j] < firstIndex[i]
SnapshotAvailable(i)    == 非空 snapshot 已应用、term 正确并覆盖压缩边界
SendSnapshot(i,j,...)   == NeedSnapshot /\ SnapshotAvailable
```

`SendSnapshot` 对齐本项目使用的 etcd-raft v3.7 progress 语义：发送非空 snapshot
后，目标 progress 进入 `StateSnapshot`，`pendingSnapshot=snapshotIndex`，且
`nextIndex=pendingSnapshot+1`；`matchIndex` 在 follower 确认前不前进。
当前 term 的成功 `MsgAppResp` 只有在 `mIndex >= pendingSnapshot` 时才清除 pending。
`LeaderProgressSafe` 检查 Leader 行上的 `match < next <= Len(log)+1`、pending
边界以及 `NeedSnapshot => SnapshotAvailable`。

Adapter 在 Leader Observation 中暴露以 peer ID 字符串为键的 `leader_progress`，并在
自然产生的 `raft.snapshot_sent` Effect 中记录 target、match、next、pending 和
progress state。Go Mapper 将该 Effect 严格映射为 `SendSnapshot`；缺字段、越界、
目标不在 Server、非 `StateSnapshot` 或 pending/next 不一致都会拒绝。

## 模型与受控检查

- 单节点完整 `StorageSpec`：76 states generated，48 distinct，depth 11，无错误；
- 两节点聚焦 `ProgressCheckSpec`：4 states generated/distinct，depth 4，无错误，
  完整覆盖 CreateSnapshot -> CompactLog -> SendSnapshot；
- strict controlled trace：合法发送成功，未创建/压缩 snapshot 时提前
  `SendSnapshot` 返回 `disabled_action`；
- strict server 现在识别26个动作定义（新增 `SendSnapshot`）。

不能直接把基础 `StorageNext` 扩成两节点全消息枚举：其消息参数没有网络发送方
约束，会允许两个 follower 互相发送现实中不可能的冲突 AppendEntries，进而违反
基础模型已有的 `LogMatching`。因此第二阶段使用聚焦穷举检查新增状态机，多节点
协议组合则由来自真实 etcd-raft 的 strict controlled trace 验证。

## 真实 etcd-raft + strict TLC 矩阵

使用正常多数派、`snapshot_threshold=2`、定向 `snapshot-partition` 策略；每种组合
运行3个 seed：

| 节点 | retain | 成功/失败 | Actions | 模型事件 | 不同 TLC 状态 | SendSnapshot | TLC 失败 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 3 | 0 | 3/0 | 63 | 69 | 55 | 3 | 0 |
| 3 | 1 | 3/0 | 63 | 69 | 55 | 3 | 0 |
| 5 | 0 | 3/0 | 108 | 120 | 97 | 3 | 0 |
| 5 | 1 | 3/0 | 108 | 120 | 97 | 3 | 0 |

12次运行全部以 `policy_complete` 结束，`failure.json=null`。每条运行自然产生一次
snapshot created/sent/applied/stale，投递两次 `MsgSnap`（原消息和复制副本）。与
第一阶段相同矩阵相比，每条轨迹恰好增加一个 `SendSnapshot` 模型事件：三节点每组
由66增至69，五节点每组由117增至120。

代表性映射为：

```text
raft.snapshot_sent(node=2, index=2, term=1,
                   to=3, match=0, next=3,
                   pending=2, state=StateSnapshot)
  -> SendSnapshot(i=2, j=3, index=2, term=1,
                  match=0, next=3, pending=2)
```

原始产物保存在忽略目录：

- `runs/snapshot-progress-phase2-3n-r0-20260722`
- `runs/snapshot-progress-phase2-3n-r1-20260722`
- `runs/snapshot-progress-phase2-5n-r0-20260722`
- `runs/snapshot-progress-phase2-5n-r1-20260722`

## 当前边界

第二阶段证明的是“Leader 只有在增量日志窗口消失且可取得合法 snapshot 时，才进入
pending snapshot progress 并发送”，并抽象成功响应对 pending 的清除。Follower 的
`Restore`、snapshot response/reject 的独立状态转换、stale/duplicate 安装拒绝仍主要
由 Concrete Trace 和 Oracle 检查；这些属于第三阶段，不能把本结果表述为完整
InstallSnapshot TLA+ 覆盖。
