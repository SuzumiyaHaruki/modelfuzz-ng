# n/3+1 选举 quorum mutant 复现实验（2026-07-21）

## 目标与配置

本实验复现原 ModelFuzz Raft 评估中的人工缺陷：把候选节点赢得选举所需的票数从
`floor(n/2)+1` 改成 `floor(n/3)+1`，并检查 NG 的随机策略、在线 Oracle 和严格
controlled TLC 能否发现错误。

三节点下两个公式都等于2，无法区分正常实现与 mutant，因此使用五节点：正常 quorum
为3，mutant quorum 为2。Go Mapper、随机策略和 TLC 均使用节点
`{1,2,3,4,5}`、`LargestTerm=10`、`MaxLogIndex=10`。TLC 配置为
`models/raft/raft-5nodes-10.cfg`，被测配置为 `examples/config-quorum-mutant.json`。

实验没有修改相邻的 `/home/nitro/Desktop/raft` 仓库。Adapter 仅在显式配置
`faults.vote_quorum_divisor=3` 时启用受控行为 mutant：候选节点实际取得2票
（包含自己的票）后，私下补充足以越过 etcd-raft 内置多数派检查的 synthetic grant。
synthetic 消息不进入 Runtime 网络和模型；外部可观察结果只是“只收到2张真实赞成票
便成为 Leader”。Adapter 记录 `raft.vote_quorum_fault_activated`，包含真实票数、正常/
错误 quorum 和 synthetic voter。正常 divisor=2 时不注入。

正确 TLA+ 模型始终保留多数派规则。激活标记映射为 stutter，随后真实出现的
`BecomeLeader` 仍必须由模型判断是否可执行，检测结果不是由标记直接制造的失败。

## 最短反例

固定 Plan `examples/plans/quorum-one-third-mutant.json` 只有3个 PlanAction：

1. 对 n1 强制 election timeout；
2. 投递 n1 -> n2 的 `MsgVote`；
3. 投递 n2 -> n1 的同意 `MsgVoteResp`。

正常配置在3个 Action 后仍保持 n1 为 candidate，映射事件为：

```text
Timeout(n1)
DeliverMessage(MsgVote, n1 -> n2)
DeliverMessage(MsgVoteResp, n2 -> n1, reject=false)
```

`runs/quorum-normal-directed-tlc-20260721` 被 TLC 接受，共返回4个模型状态。

mutant 在相同3个 Action 后让 n1 成为 leader，额外映射：

```text
BecomeLeader(n1)
ClientRequest(n1, 0)  // etcd-raft 的 leader no-op
```

`runs/quorum-mutant-directed-tlc-20260721` 在模型事件3失败：

```text
TLC disabled_action at event 3 (BecomeLeader)
```

五节点候选只有自己和 n2 两票，正确模型要求3票，因而 `BecomeLeader` 在当前模型状态
下不可执行。这是确定性的最短复现。

## 100-seed 对照

两组实验使用 base seed `470721`、100次执行、每条最多1000个 PlanAction、单线程
controlled TLC。除 `vote_quorum_divisor` 外，节点、模型边界、随机权重和预算相同。
随机策略在线读取 Observation；mutant 改变 SUT 状态后，后续可用动作和随机消费也会
变化。因此“相同 seed”保证初始 PRNG 条件相同，但不表示分歧后的 Concrete Action
仍逐项相同。严格的同输入对照由上一节固定 Plan 提供。

| 指标 | 正常 quorum | n/3+1 mutant |
|---|---:|---:|
| 目录 | `runs/quorum-normal-fuzz-100-20260721` | `runs/quorum-mutant-fuzz-100-20260721` |
| completed | 100 | 100 |
| succeeded / failed | 100 / 0 | 0 / 100 |
| status | completed=100 | model_failed=76, oracle_failed=14, runtime_failed=10 |
| total actions | 85,362 | 82,797 |
| mapped model events | 63,058 | 60,483 |
| forced timeout actions | 1,044 (1.2230%) | 973 (1.1752%) |
| proposal dropped markers | 224 | 481 |
| quorum fault activations | 0 | 339 |
| Oracle findings | 0 | committed_log_conflict=18, multiple_leaders_same_term=4 |
| model_bound_reached | 7 | 0 |
| message_not_available | 8,040 | 0 |
| peak Ready candidates | 29 | 4 |
| final checkpoint | 247,938 bytes | 50,972 bytes |

mutant 的14个 `oracle_failed` run 共记录22条 Finding；Finding 数不是 run 数。具体为
18条 committed-log conflict 和4条 same-term multiple leader。76个 `model_failed`
run 都先执行到1000 Action 预算，再在批量送入 TLC 时被拒绝。模型执行发生在 SUT
轨迹结束后，而 Oracle 在线运行，所以 Oracle 或 runtime failure 可以更早终止轨迹。

该历史 mutant 报告生成于下文 MsgProp 转发误判修复之前。10个 `runtime_failed` 为：

- 6个 `runtime_error`：转发 `MsgProp` 保留原请求者 `From`，原请求者后来崩溃时，
  Adapter 错把已在途 proposal 判断为“crashed sender 新发消息”；这是框架假失败；
- 4个 `sut_panic`：`need non-empty snapshot`；这是弱 quorum 造成不安全日志状态后的
  真实 SUT 下游崩溃，机制见下一节。

该批次直接得到的目标缺陷证据为76次模型拒绝、14次 Oracle 失败和4次 SUT panic，
共94/100；另外6次被框架误判提前截断，不能从旧报告中直接重新分类。修复后以 seed
`470723` 回归：原来在 action 352 的 sender-crashed 错误消失，轨迹执行满1000
Actions；连接本地 TLC 后在 event 12 的 `BecomeLeader` 变成 `model_failed`。产物为：

- `runs/quorum-mutant-seed-470723-after-forward-fix-20260721`；
- `runs/quorum-mutant-seed-470723-after-forward-fix-tlc-20260721`。

尚未用修复后的代码重跑完整100-seed 批次，因此不能把这一个回归样本外推并改写旧报告
的完整状态分布。

## 随机发现样本

seed `470724` 的完整产物位于：

- 正常：`runs/quorum-normal-seed-470724-20260721`，1000 Actions，成功，118个模型状态；
- mutant：`runs/quorum-mutant-seed-470724-20260721`，1000 Actions，TLC
  `disabled_action`。

mutant 在零基 step 12（第13个 Action）投递 n4 -> n3 的 term 2 `MsgVoteResp`。
n3 当时只有自己的票和 n4 的票：`actual_grants=2`、`faulty_quorum=2`、
`normal_quorum=3`。随后映射事件11出现 `BecomeLeader(n3)`，正确模型立即拒绝。
这条轨迹来自随机在线策略，不是手写最短 Plan。

## `need non-empty snapshot` 机制

用旧批次中的 seed `470729` 进行一次仅本地、无 TLC 的确定性重跑，产物位于
`runs/quorum-mutant-seed-470729-analysis-20260721`。action 258 投递 m187 时复现相同
panic，调用链为：

```text
stepLeader
  -> maybeSendAppend
  -> maybeSendSnapshot
  -> panic("need non-empty snapshot")
```

panic 前的关键状态是：

- n2 因弱 quorum 在 term 10 成为 leader，但自身日志只到 index 4，commit=0；
- n5 的日志到 index 10，且已 commit/apply 到 index 9；
- n2 向 n5 发送 `MsgApp(prevIndex=2, prevTerm=2)`；
- 因 `prevIndex < n5.committed`，n5 返回成功的 `MsgAppResp(index=9)`；
- n2 将 n5 progress 更新为 `Match=9, Next=10`，但 n2 本地没有 index 9；
- n2 尝试从 Next=10 继续复制，读不到本地日志，于是转向发送 snapshot；
- snapshot policy 为0，n2 的 `MemoryStorage` 只有 index 0 的空初始 snapshot，最终
  触发 `panic("need non-empty snapshot")`。

这不是普通合法 Raft 状态下的独立 snapshot 路径。它由错误 quorum 允许落后节点成为
Leader、破坏 Leader Completeness 后触发，应保留为 mutant 的真实失败信号。当前证据
不足以把它宣称为与 quorum mutant 无关的第二个独立缺陷。

## 统计口径

- `total_actions` 是成功执行的 Concrete Action 数；timeout 占比使用
  `action_counts.timeout / total_actions`。模型 `Timeout` 事件还包含自然 election
  timeout，不能替代这个分母。
- `model_event_counts` 同时统计 Adapter 原始标记和映射后的 TLA+ 事件。例如
  `raft.message_delivered` 与 `DeliverMessage` 是同一次投递的不同层次，不能把各项求和
  当成 `total_model_events`。
- `raft.proposal_dropped` 是显式 stutter 标记。两组共出现705次，没有任何一次以
  `ErrProposalDropped` 计入 runtime failure。
- mutant 的 `unique_model_states=0` 和空 Corpus 不表示没有探索状态；当前流水线只从
  成功 TLC 响应采纳状态，失败响应不返回反馈状态列表。
- mutant 没有 Corpus 和 mutation，也就没有带过期 selector 的变异 Plan；其
  `message_not_available=0` 不能与正常反馈组的8,040解释为 mutant 改善。
- 正常组 Ready 峰值29，远低于上限4096。100-run checkpoint 约248 KB；mutant 因无
  Corpus 约51 KB。它们证明 checkpoint v6 在本实验保持紧凑，但不能单独证明10万轮上界。
- 强制 timeout 占比约1.2%，相对旧两小时实验约27%的观察值明显下降。不过集群规模、
  候选来源和终止分布不同，这里只作方向性比较，不作严格性能因果结论。

## 结论

系统已用三条独立信号复现 n/3+1 quorum 缺陷：最短固定 Plan 的 TLC
`disabled_action`、随机轨迹的相同模型拒绝，以及在线 Oracle 的 committed-prefix/
same-term-leader 违反。正常五节点同边界100-seed 对照全部成功，说明错误不是由五节点、
10/10模型边界或1000-Action预算本身引入。

MsgProp sender-epoch 假失败已修正并由原失败 seed 验证；旧100-seed报告保持为历史记录。
若需要发布正式检测率，应在该修正后用相同 base seed 完整重跑100次，再单独研究空
snapshot panic 是否能构造不依赖 quorum mutant 的最小 Plan。
