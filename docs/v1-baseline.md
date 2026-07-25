# ModelFuzz-NG 正式 v1 基线

CLI 发布版本为 `v1.0.0`，可通过 `modelfuzz-ng version` 查询。

本文件定义从正式 v1 开始生效的持久化边界。此前开发阶段生成的 checkpoint、Trace、
semantic key 和 `runs/` 原始实验数据均不提供兼容性保证。

## Schema

| 产物 | 正式版本 | 兼容策略 |
|---|---:|---|
| Trace | 1 | 仅接受 v1，必须包含前后节点快照和 observation digest |
| Experiment checkpoint | 1 | 仅接受 v1，并校验完整实验配置指纹 |
| Raft semantic coverage | `raft-coverage-v1` | schema 写入实验设置和配置指纹 |
| Minimizer checkpoint | 1 | 仅恢复相同最小化任务 |
| etcd-raft snapshot payload | 1 | Adapter 内部稳定快照编码 |
| strict TLC server API | 1 | health 返回 server version 1 |

正式 v1 不包含 pre-v1 artifact 迁移代码。尝试恢复不同 checkpoint 或 semantic schema
会显式失败，不会静默复用旧 Corpus key。

## 当前能力边界

正式 v1 基线包括：

- 真实进程内 etcd-raft、确定性消息队列与逻辑时间；
- Deliver、Drop、Duplicate、Timeout、Request、Crash、Restart、Partition、Heal；
- strict controlled TLC 映射、Raft Oracle、Corpus feedback、mutation、checkpoint、
  replay 和 minimization；
- Storage/Snapshot 的 create、compaction、send、install、failure/retry、FastForward 和
  snapshot status 模型映射；
- 三节点和五节点配置，以及受控 quorum mutant。

动态 membership、PreVote、真实磁盘/WAL 故障、外部进程 backend、跨协议插件边界和
节点对称归约不属于 v1 保证范围。

## 数据起点

正式 v1 的 `runs/` 从空目录开始。`experiment-settings.json` 会记录 `release_version`
和 semantic schema。后续实验必须由当前 v1 二进制重新生成，报告中应记录代码
revision、Trace/checkpoint/semantic schema、TLC server version、模型 profile、配置和
seed 范围。
