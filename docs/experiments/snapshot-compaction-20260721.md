# etcd-raft Snapshot 与日志压缩实验（2026-07-21）

## 设计边界

etcd-raft 不会代替应用调用 `MemoryStorage.CreateSnapshot` 和 `Compact`。NG Adapter
新增默认关闭的 `SnapshotPolicy{Threshold, RetainEntries}`，使用已应用日志数量
而非物理时间触发。Snapshot Metadata.Index 等于 applied index，压缩点为
`snapshotIndex-RetainEntries`。

Snapshot Data 是确定性 JSON，携带从0到 snapshot index 的 committed-prefix 链式摘要。
因此 follower 应用 snapshot 后可恢复 Oracle 所需前缀，无需从已压缩的 index=1 读日志。
Effect 只记录 index、term、size、compact index 和压缩数量，不重复持久化 payload。

固定三 voter 实验使用完整 ConfState。现有 conf-change 基础路径仍保留，但动态成员变更后的
Snapshot ConfState 组合未做端到端验证，因此本轮不声称支持动态 membership。

## 消息与模型

`MsgSnap` 由 Leader 在 follower nextIndex 早于 FirstIndex 时自动产生，路径为：

```text
Ready.Messages -> EffectSendMessage -> Runtime queue
-> Deliver/Drop/Duplicate -> RawNode.Step(MsgSnap) -> Ready.Snapshot
```

基础 `models/raft/raft.tla` 没有 snapshot 变量，因此 snapshot lifecycle Effect 和 MsgSnap
明确映射为 stutter。原 artifact 的 `raft_enhanced.tla` 只抽象
`UpdateSnapshotIndex(i, si)`，不是完整 InstallSnapshot 模型；本轮没有引入一个名不副实的
“增强模型”。

## 测试与 Plan

单元/集成测试覆盖：默认关闭、Threshold/RetainEntries 边界、创建与压缩、
crash/restart 保留存储、snapshot data 损坏/前缀冲突、MsgSnap 入队、复制投递、stale 拒绝、
follower 应用后继续追赶，以及普通 Raft Profile 的 stutter 分类。

三条完整 Plan 均以0个 Oracle finding 完成：

- `snapshot-normal.json`：11个 Concrete Action，Leader/Follower 各创建并压缩1次；
- `snapshot-follower-catchup.json`：28个 Action，观察到 snapshot created/sent/delivered/applied，
  follower 恢复后追赶；
- `snapshot-duplicate-delivery.json`：20个 Action，第一份 MsgSnap 应用，第二份记录
  `raft.snapshot_rejected_or_stale`。

重复运行第三条 Plan 的 `trace.json` SHA-256 均为
`ef73a845e3fd58858575045f5d2788b5816ec4a5f2ef29b5160f0a64da730337`，并且严格 Replay
匹配20/20个 Step。
关闭 snapshot 后，新版与修改前 `client-request-commit` 的 trace SHA-256 完全一致：
`37eafca17b97c3084246aeed57abcfc345e9bd29e2a5c2b2be73a963a10e17da`。

## 实验结果

无 TLC，`seed=470721`，50条、每条30 Action：

```text
runs/snapshot-feedback-no-tlc-50-20260721
succeeded=50 failed=0 actions=1500 throughput=640.48 actions/s
snapshots_created=48 snapshots_sent=9 snapshots_delivered=1
snapshots_applied=0 logs_compacted=48 compacted_entries=104 snapshot_bytes=12736
runtime_failure=0 oracle_findings=0 checkpoint=27414 bytes
```

普通 10/10 严格 TLC，30条：

```text
runs/snapshot-feedback-tlc-30-20260721
succeeded=30 failed=0 actions=811 unique_model_states=141
snapshots_created=17 logs_compacted=17 compacted_entries=42 snapshot_bytes=4667
runtime_failure=0 oracle_findings=0
```

这两个随机实验中 MsgSnap 投递较少，因此安装/重复语义以上述定向 Plan 和集成测试为准。

checkpoint 实验在第6条后收到 SIGTERM，随后恢复到100条，并与未中断对照比较。
删除时长/吞吐字段后的 report SHA-256 完全一致：
`58c5036e9cf161ee4777b4bcfcf8e97876674f3722863f698a78f8841bde0c16`。
checkpoint 约50KiB，SnapshotPolicy 已进入 config 指纹，resume 禁止通过 CLI 修改。
