# etcd-raft 网络分区与合并实验（2026-07-22）

## 语义

本轮新增协议无关的 `partition` 和 `heal` Action。Partition 使用两个或更多非空 group，并要求当前 Observation 中的每个节点恰好出现一次；组内链路保持可投递，跨组消息继续获得稳定 MessageID、LinkSequence 和入队时间，但 Observation 标记 `blocked=true`，Resolver、Raft Profile 和 Runtime 三层都会拒绝跨组 Deliver。Drop 和 Duplicate 仍可作用于 blocked 消息。Heal 清除当前分区，不重排也不重建队列，积压消息按原 ID 和位置重新可投递。

Runtime 当前只允许一个活动 partition；再次 partition 或在无 partition 时 heal 会被 Plan Resolver 稳定分类为 `partition_already_active` / `partition_not_active`，直接绕过 Resolver 的 Concrete Action 则返回 `ErrPartitionState`。基础 Raft TLA+ 模型没有网络拓扑变量，因此 partition/heal 自身明确 stutter；它们造成的 term 变化、选举、AppendEntries 和 commit 仍由后续真实消息事件进入模型。

## 生成与恢复

在线随机策略对当前节点集合枚举去除 A/B 对称后的二分组：三节点有3种，五节点有15种。默认 `partition-weight=2`、`heal-weight=8`，避免长时间停留在分区状态；本地 Mutation 默认以5%概率插入包围至少一个已有 Action 的 partition/heal 对，并拒绝嵌套分区和无分区 heal。LLM schema 和校验器使用同一 groups 表示。checkpoint v8 将三个网络故障参数纳入 Config 和配置指纹，恢复时禁止修改。

## 三节点固定轨迹

`examples/plans/network-partition-merge.json` 先让 n1 在 term=1 成为 Leader并提交 no-op，再建立 `{n1}|{n2,n3}` 分区。n1 在少数派接受但不能提交请求；n2 在连通组内于 term=2 当选并提交日志。Heal 后投递双方积压消息，最终 n1 降级为 n2 的 Follower，三个节点均为 term=2、commit=applied=lastIndex=3、snapshotIndex=2，committed-prefix digest 完全一致，Oracle finding=0。

| 指标 | 结果 |
|---|---|
| Concrete Action | 43 |
| partition / heal | 1 / 1 |
| 最终活动分区 | 无 |
| 最终三节点 commit/applied | 3 / 3 |
| Oracle finding | 0 |
| 严格 Replay | matched_steps=43，completed |

运行产物为 `runs/network-partition-merge-v2-20260722`，Replay 为 `runs/network-partition-merge-replay-20260722`。

## 五节点随机 smoke

新增正常多数派、10/10边界并启用 snapshot 的 `examples/config-5nodes-snapshot.json`。无 TLC 随机实验执行50条、每条120 Action，显式提高 partition/heal 权重以增加命中率。

| 指标 | 结果 |
|---|---|
| succeeded / failed | 50 / 0 |
| Action | 6,000 |
| partition / heal | 219 / 205 |
| Deliver / Drop / Duplicate | 3,698 / 344 / 314 |
| snapshot created/sent/delivered/applied | 86 / 31 / 15 / 12 |
| Oracle finding | 0 |
| 最大队列 | 70 |

产物位于 `runs/network-partition-5nodes-smoke-50-20260722`。该 smoke 验证五节点下 topology、队列、snapshot 和 Oracle 能共同运行，不代表完整 InstallSnapshot TLA+ 验证，也不包含成员变更。

## Checkpoint v8 恢复

五节点30条对照在完成3条后由 SIGTERM 产生 `context canceled` 并保存 checkpoint v8，随后从同一目录恢复到30条。恢复实验与相同 seed/config 的不中断 control 都完成3,600 Action、2,678 model event，partition/heal=134/128、snapshot applied=6、失败和 Oracle finding 均为0。删除时长、吞吐和 coverage timeline 后，两份 report SHA-256 同为 `0679f515b736b81a294159e166f5689ae99667e38d16b7176d94e93df416376b`；删除逐运行时长后，两份 `runs.jsonl` SHA-256 同为 `3a20c29525961e911f79fb7ce14812228b7062dea3b0065fad5404d860c7470a`。这验证 partition/heal 的随机调度参数、Plan 和 Trace 在中断恢复后保持确定性。

恢复产物为 `runs/network-partition-resume-30-20260722`，不中断对照为 `runs/network-partition-control-30-20260722`。
