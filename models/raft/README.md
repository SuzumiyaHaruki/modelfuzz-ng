# Raft 模型

该目录保存 ModelFuzz-NG 的基础 Raft 模型与 Storage/Snapshot 扩展模型。
基础模型沿用原 ModelFuzz
`raft_alt.tla` 的核心思路：网络队列由 Runtime 控制，TLA+ 模型本身不维护
message bag；实际消息投递由 `internal/model/raft.Mapper` 转成具体消息处理动作。

当前模型覆盖选举、成为 leader、客户端请求、日志复制和 commit index 推进，
可以支持第一条端到端轨迹：

```text
Timeout
  -> Deliver MsgVote
  -> Deliver MsgVoteResp
  -> BecomeLeader
  -> ClientRequest(leader no-op)
```

## 模型 Profile

- `basic`：`raft.tla` 与 `raft-{5,10}.cfg`。保持原有状态空间和历史实验可比性，
  snapshot lifecycle 与 `MsgSnap` 都明确 stutter。
- `storage-snapshot`：`raft_storage_snapshot.tla` 与
  `raft-storage-snapshot-{5,10}.cfg`。扩展基础模型但不复制其协议定义，新增
  `appliedIndex`、`snapshotIndex`、`snapshotTerm`、`firstIndex` 和
  `pendingSnapshot`，并映射 `ApplyCommitted`、`CreateSnapshot`、
  `CompactLog`、`SendSnapshot`、`InstallSnapshot`、`FastForwardSnapshot`、
  `RejectSnapshot`、`HandleSnapshotStatus`。

`raft-storage-snapshot.cfg` 是1节点、1/1边界的完整 `StorageSpec`，用于快速穷举
模型本身；带 `-5`、`-10`、`-5nodes-10` 后缀的配置使用
`StorageControlledNext`，供真实多节点轨迹按事件严格执行。
`raft-storage-snapshot-progress.cfg` 是两节点聚焦穷举配置，从合法 Leader progress
边界检查 Create/Compact/Send 的次序。

Storage/Snapshot 模型保留完整 `log` 作为 ghost history，压缩只移动
`firstIndex`，不会从逻辑日志删除前缀。`SnapshotStorageBoundary` 检查：

```text
snapshotIndex <= appliedIndex <= commitIndex <= Len(log)
firstIndex - 1 <= snapshotIndex
snapshotTerm = log[snapshotIndex].term（snapshotIndex > 0）
```

这能验证“只对已应用前缀创建快照”和“只压缩已被快照覆盖的日志”。第二阶段
还定义 `EntryAvailable`、`NeedSnapshot`、`SnapshotAvailable` 和 `SendSnapshot`，
复用 `nextIndex/matchIndex` 并跟踪 Leader 的 `pendingSnapshot`；成功且覆盖 pending
边界的当前 term `MsgAppResp` 会清除 pending。第三阶段进一步用 source ghost log
prefix 表达 Follower Restore，并区分 matching-entry fast-forward 与旧/重复 snapshot
拒绝。第四阶段进一步覆盖成功/失败 `MsgSnapStatus`、失败后的 progress 回退和重试；
heartbeat 等待节奏仍抽象为 stutter。固定 voter 的安装路径已进入模型；ConfState
动态恢复和损坏 payload 仍未建模。

## 使用约束

- 默认配置是 3 节点，term、日志长度和请求值均有界；规模变化时应从同一份
  实验配置生成 `raft.cfg`、Adapter Config 和 Mapper Config。当前 Mapper 对应
  字段为 `NodeIDs`、`MaxValue`、`MaxLogIndex`、`LargestTerm`。
- `raft-5.cfg` 是5/5烟雾配置，`raft-10.cfg` 是原 ModelFuzz 主实验使用的10/10
  配置；`raft-5nodes-10.cfg` 使用五节点和10/10边界，供选举 quorum mutant
  对照实验使用。这些配置的 `ControlledNext` 专供严格 HTTP 服务按事件创建 Action，
  不在 Tool 启动时枚举全部参数组合。兼容文件 `raft.cfg` 仍使用5/5边界的
  完整 `Spec`，可用于普通 TLC 枚举。使用 controlled TLC 时必须让 Go JSON/CLI 边界
  与所选 cfg 一致，严格服务会在 `/health` 暴露实际值供 CLI 自动核对。
- 使用扩展模型时，Go JSON 必须设置 `model.profile=storage-snapshot`，并启动
  `raft_storage_snapshot.tla`。strict server 从本地 `Storage*` 动作识别 profile，
  `/health.model_profile` 与 Go 配置不一致时 CLI 会在执行前拒绝。
- 客户端请求暂时必须是 `1..MaxValue` 的十进制整数。`0` 保留给 etcd-raft
  成为 leader 时产生的 no-op entry。Follower 已知当前 Leader 时会产生受控
  `MsgProp`；只有该消息投递到当前 Leader 时，Mapper 才生成 `ClientRequest`。
  Candidate 或无已知 Leader 的 Follower 丢弃 proposal，并明确映射为 stutter。
- 模型处理 `MsgVote`、`MsgVoteResp`、`MsgApp`、`MsgAppResp`。`MsgHeartbeat`
  被显式抽象为无 entry 的 `MsgApp`，从而保留 term、角色降级和 commit 传播；
  当前配置下确实不改变模型状态的响应/只读消息被显式标记为 stutter。
- controlled TLC 的旧 `RaftActionMapper` 一次只接受一条 entry。NG Mapper 会先
  根据真实 `MsgAppResp` 判断原子批次是否成功：成功的多 entry `MsgApp` 按日志
  顺序展开为多个单 entry 模型事件；被拒绝的批次只映射前缀不匹配动作，避免
  后续 entry 被模型错误接受。展开产生的中间状态只存在于模型侧，不对应额外
  的 SUT Action。
- 更高term节点可能直接忽略旧term的多 entry `MsgApp` 且不发送响应。Mapper
  将这种节点状态未变化的输入缩减为第一条 entry 对应的模型 stutter，不再把
  “没有响应”误判成映射失败。
- 未知消息、未建模的 snapshot/ConfState 异常路径、membership change 等超出轻量模型表达能力的转换会返回
  错误，不会静默忽略。
- 模型使用 `currentActive`、`RemoveFromActive` 和 `AddToActive` 表达
  crash/restart；崩溃保留稳定状态，恢复会重置节点的 Raft 易失状态。
- membership change 和 PreVote 尚未建模，Mapper 会明确拒绝对应语义。
- `partition`/`heal` 由 Runtime 管理，在基础模型中明确映射为 stutter；分区造成的后续选举、消息投递、日志复制和提交仍按现有模型动作检查。
- 更准确地说，基础模型不包含 snapshot 变量或 InstallSnapshot 状态转换；
  Adapter 产生的 snapshot 创建/发送/投递/应用/压缩 Effect 和 `MsgSnap` 被稳定归类为
  model stutter。这保持基础模型可用，但不等于形式化验证了 snapshot install。
- 原 artifact 的 `raft_enhanced.tla` 只提供 `snapshotIndex`/
  `UpdateSnapshotIndex(i, si)` 抽象，也不是完整 InstallSnapshot 协议。NG 的
  `storage-snapshot` profile 额外建模 applied/term/first-index、Leader progress 和
  固定 voter 的 Follower install；动态成员与异常 payload 仍是明确边界。
- `TypeOK` 对 term、日志 entry、日志长度、commit、投票集合以及 leader 复制索引
  做完整有界检查；`CommittedPrefixAgreement` 和 `LogMatching` 分别检查已提交
  前缀一致性与 Raft 日志匹配性质。

当前支持的执行路径是仓库内的 strict TLC server；不需要原 artifact 的
`ActionMapperFactory`。扩展模型启动示例：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft_storage_snapshot.tla \
  --config models/raft/raft-storage-snapshot-5.cfg \
  --port 2023
```
