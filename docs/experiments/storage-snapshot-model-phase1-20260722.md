# Storage/Snapshot TLA+ 第一阶段（2026-07-22）

## 目标

第一阶段只形式化本地 Storage 与 Snapshot 边界，不提前宣称覆盖完整
InstallSnapshot 协议。基础 `raft.tla` 保持不变，新增
`raft_storage_snapshot.tla` 通过 `EXTENDS raft` 复用选举、复制和提交语义。

## 状态与动作

扩展状态：

- `appliedIndex`：应用层已经消费的最大 committed index；
- `snapshotIndex` / `snapshotTerm`：最近创建的本地 snapshot 边界；
- `firstIndex`：底层 Storage 仍可读取的第一个日志索引；
- 基础 `log` 不物理截断，作为验证 snapshot term 和 committed prefix 的 ghost history。

扩展动作：

- `ApplyCommitted(i, index)`：只允许应用到 `commitIndex` 以内；
- `CreateSnapshot(i, index, term)`：只允许覆盖已应用日志，term 必须匹配 ghost log；
- `CompactLog(i, index)`：只允许压缩已由 snapshot 覆盖的前缀，并把
  `firstIndex` 推进到 `index+1`。

基础 Raft 动作由 `Storage*` 包装器执行并显式保持扩展变量，因此 strict TLC 每个
事件仍然必须产生唯一、完整后继。

## 不变量

`SnapshotStorageBoundary` 检查：

```text
snapshotIndex <= appliedIndex <= commitIndex <= Len(log)
firstIndex - 1 <= snapshotIndex
snapshotTerm = log[snapshotIndex].term（snapshotIndex > 0）
```

原有 `OnlyOneLeader`、`CommittedPrefixAgreement`、`LogMatching` 继续同时检查。

## 接入

Go 配置通过 `model.profile = "storage-snapshot"` 启用扩展映射。strict server
从模型中的 `Storage*`/`CreateSnapshot` 动作识别 profile，并在 `/health` 返回
`model_profile`；CLI 会拒绝 profile 不一致的连接。空 profile 仍等价于 `basic`，
保持旧 checkpoint JSON 与基础实验兼容。

## 验证结果

- 1节点、`MaxLogIndex=1`、`LargestTerm=1` 完整状态空间：76个生成状态、
  48个不同状态、深度11、队列清空，无 invariant 错误；
- strict TLC 受控轨迹覆盖选举、Leader no-op、复制响应、Apply、CreateSnapshot、
  CompactLog，10个状态均产生唯一后继；
- 从初始状态直接 `CompactLog` 被稳定拒绝为 `disabled_action`；
- 基础 `raft.tla` lazy/eager Action 等价回归继续通过；
- Go Mapper 测试覆盖 basic stutter、扩展事件顺序、越界拒绝以及安装协议继续 stutter。

真实 etcd-raft + strict TLC 的3/5节点、retain=0/1矩阵另见
[`storage-snapshot-model-e2e-20260722.md`](storage-snapshot-model-e2e-20260722.md)：
12条定向轨迹全部 `policy_complete`，Apply/Create/Compact 均自然进入 TLC，未出现
模型、Oracle 或 Runtime failure。

## 明确未覆盖

`MsgSnap` payload、snapshot 发送/投递、Follower InstallSnapshot、stale snapshot
拒绝、安装后的日志重建和 progress/nextIndex 联动不在第一阶段内。这些事件仍由
Concrete Trace、指标和 Oracle 记录检查；TLA+ 中稳定 stutter，留给下一阶段。
