# 定向 Snapshot/Partition 与失败 Plan 缩减（2026-07-22）

## 结论

本轮为 etcd-raft 增加了观察驱动的 `snapshot-partition` 在线策略和保持稳定失败签名的 Plan 缩减器。前者能稳定制造“Follower 被隔离、Leader 多数派提交并压缩、heal 后只能通过 MsgSnap 追赶、重复 snapshot 被判 stale”的具体生命周期；后者把历史 missing-snapshot panic Plan 从259个 Action 缩到46个，并支持候选缓存、最终独立复现和中断恢复。基础 TLA+ 模型仍把 partition/heal 与 snapshot lifecycle 映射为 stutter，后续真实选举、复制和提交事件继续进入模型；Concrete Oracle 负责 snapshot 边界和 committed prefix。

## 在线状态机

| 阶段 | 观察条件 | 动作 |
| ---: | --- | --- |
| 1 | 尚无 Leader | 对 seed 选择的节点 timeout，并投递实际选举消息 |
| 2 | Leader 已出现 | 隔离最大非 Leader 节点作为 lagger |
| 3 | 分区仍在 | 在多数派投递消息并提交请求，等待 snapshot/compaction |
| 4 | `leader.first_index > lagger.last_index+1` | heal；该条件证明 lagger 所需的下一条日志已不可用 |
| 5 | heal 后旧 MsgApp/heartbeat/response 在队列中 | 丢弃旧增量消息并驱动 Raft 产生实际 MsgSnap |
| 6 | MsgSnap 出现 | 可选 Duplicate，依次投递原件和副本 |
| 7 | lagger snapshot/commit/applied 追上 Leader | 以 `policy_complete` 结束 |

策略每一步都从最新 Observation 按稳定 MessageID 顺序选择已经存在的消息，不提前预测 MessageID，因此分区、Drop、Duplicate 和 Ready 产生的新消息不会使后续选择失效。当前默认只隔离一个 lagger、只使用二分区、复制一份 MsgSnap、recovery tick budget 为16，并只保证 Leader 与目标 lagger 收敛。

## 定向实验与 retain 边界

| 配置 | runs | 结果 | snapshot 指标 |
| --- | ---: | --- | --- |
| 5节点、retain=0、20 seeds | 20 | 20成功、0失败、全部 `policy_complete` | created/sent/applied/stale 各20，delivered 40 |
| 3节点、retain=0、15 seeds | 15 | 15成功、0失败、全部 `policy_complete` | created/sent/applied/stale 各15，delivered 30 |

原始多 seed 数据只证明 retain=0。修复后真实 etcd-raft CLI 回归覆盖3节点 retain=0、1、threshold，以及5节点 retain=0、1；DuplicateSnapshot=true 的 CLI 端到端路径会产生 delivered=2、applied=1、stale=1，DuplicateSnapshot=false 的真实 etcd-raft Engine 路径会产生 delivered=1、applied=1、stale=0。策略不再用 `snapshot_index > lagger.last_index` 近似判断，而是观察 Leader `first_index`；若 `MaxLogIndex` 内不可能让 snapshot 压缩超过 retain 窗口，构造策略时直接返回配置错误。

## 中断恢复确定性对照

新增回归以2条 `snapshot-partition` 在线候选建立未中断 control，在第1条完成并写入 checkpoint 后取消另一组实验，再从 checkpoint 恢复第2条。恢复执行与 control 使用相同 run index、seed 和 ExecutionID，最终逐字段比较 PlanSequence、Concrete ActionSequence 和完整 Concrete Trace，三者完全一致；这同时验证 checkpoint 保存的 nil Plan 在线候选会按原策略名和 seed 重建，而不会被误当成默认 random 策略。

## 历史 panic 缩减

| 阶段 | PlanAction | 尝试 | 结果 |
| --- | ---: | ---: | --- |
| 原始反例 | 259 | — | `runtime_failed/sut_panic/deliver`，panic=`need non-empty snapshot` |
| 第一轮 | 63 | 600 | 达到尝试上限，未声明1-minimal |
| 第二轮 | 46 | 603 | 对单 Action 删除算子达到固定点 |
| 独立复现 | 46 | 3次 | 3/3保持完全相同 panic 签名 |

最终 Plan 有46个 PlanAction，而 `result.json` 只含45个成功 Concrete Action：第46个 Deliver 在形成合法 After Observation 和 Trace Step 前触发 panic，失败动作单独保存在 `failure.action`。`one_minimal=true` 只表示删除任意一个现有 Action 都不能保持签名，不证明不存在需要同时替换、重排或删除多个 Action 的更短反例。

该反例依赖 `vote_quorum_divisor=3` 且 snapshot policy 关闭，复现的是原 ModelFuzz 所述源码位置的同一 missing-snapshot panic 机制；它不能证明正常多数派、正确 snapshot 契约下存在独立的 etcd-raft 上游缺陷。

## 缩减器签名与恢复

| 失败类别 | 稳定签名 |
| --- | --- |
| TLC | error code + 模型动作名；忽略 event index |
| mapping/resolution/policy | 稳定 reason/error 类别；忽略 Action 下标和动态数值 |
| runtime error | operation + 归一化根因类别 |
| SUT panic | operation + 精确 panic value |
| Oracle | 排序去重后的 `oracle:code` 集合 |

缩减器不修改原 Plan Metadata；报告单独记录 SHA-256、尝试次数、cache hits、最终稳定复现次数和1-minimal状态。每次新候选执行或接受缩减后原子更新 `minimization-checkpoint.json`，其中保存当前 Plan、累计尝试和候选 digest/结果缓存；`minimize -resume DIR` 会校验保存的 Plan/config 摘要后继续。controlled TLC 仍保持串行，缩减器不并发复用服务状态。

## TLC 启动边界

严格 TLC `/health` 现暴露 `server_ids`、`largest_term`、`max_log_index`、`max_value` 和 `nil_value`。Go CLI 在实验、运行和缩减启动前比较 Server 集合、LargestTerm、MaxLogIndex、MaxValue，并在 Nil 非0时拒绝；因此五节点 Go 配置误连三节点 cfg 会立即失败，不再等到 mapping/disabled_action。旧服务只暴露 term/log 时继续兼容，但会输出无法核对 Server/MaxValue 的明确警告。
