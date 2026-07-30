# Partial Branch Evidence 与阶段预算原型

本文记录第六轮“Branch 形成障碍诊断、Partial Causal Evidence 与阶段预算原型”的设计、实现和本地受控实验。所有 fault injection 都发生在进程内 etcd-raft 测试环境；没有调用 LLM，也没有访问外部目标。

文中的证据类型严格分开：

- **个案诊断**：解释一个已知成功 seed 为什么成功，不代表统计优势；
- **能力诊断**：给单条 Branch 独占预算，回答“能不能形成”，不用于公平性能排名；
- **Pilot**：用于发现实现问题和冻结参数，不作为正式效果结论；
- **公平正式实验**：各方法使用相同 candidate/action 上限，用于方法比较；
- **Mutant 诊断**：测试已知人工缺陷及其 control，评价实际检错能力；
- **设计推测**：有机制解释但证据还不充分；
- **未运行组合**：不得当作负结果；
- **不能推出的结论**：当前样本不支持泛化到所有 Raft 或共识系统。

## 1. 本轮动机

第五轮已经能描述 Planned Branch 和完整 Realized Branch，但 weak 搜索几乎不能形成完整路径。只增加 Branch 数、Frontier 容量或语义维度并没有解决问题。本轮先回答三个更基础的问题：

1. 完整 Branch 形成前，哪些已经发生的事件是真正的因果准备？
2. Frontier 应该保留哪些前缀，而不是只保留“看起来不同”的前缀？
3. 固定总预算下，怎样避免在许多浅 Branch 之间平均轮换？

## 2. 第五轮负结果

第五轮的两个 weak Diversity M4 均为 0/10；400 次 Planned Branch 尝试没有形成完整 Realized Branch。Goal A 的四条可行路径最深只到 W2，Goal B 的 Heartbeat 最深 W3、MsgApp/Vote 最深 W2。C=2 只有 seed 4101 成功，容量增加到 4 或 8 没有单调收益。Restart mutant 有局部积极信号，但 Snapshot mutant 仍依赖 strong 上界。

这些结果说明“语义种类更多”不等于“因果路径推进更多”。

## 3. 为什么完整 Realized Branch 不应放宽

完整 Realized Branch 回答的是：一条具体执行是否已经用真实 Action、Effect、消息和状态变化，证明了 Branch Catalog 中要求的全部比较维度。Planned label、可行性、局部 Evidence 或较深 Waypoint 都不能替代这项证明。

本轮保留原定义：

```text
Planned Branch
  → Supported Partial Evidence
  → Branch Commitment
  → RealizedDecidable
  → Full Realized Branch
```

如果实际消息类别与 Planned Branch 不同，会记录 contradiction/deviation；即使同一次执行按实际证据形成了另一条 Full Realized Branch，也不能把 planned 与 realized 的不一致抹掉。

## 4. C=2 成功 seed

第五轮唯一的 weak C=2 成功个案是 Goal B、seed 4101。第六轮按相同输入完整重跑七个配置，接受的原始目录为：

`/tmp/modelfuzz-ng-round6-c2-differential-raw-v2-20260728`

离线差分目录为：

`/tmp/modelfuzz-ng-round6-c2-analysis-v4-20260728`

七个配置共生成 1409 条逐 Action 对齐记录，online/offline mismatch 为 0，prefix replay 为 96/96，LLM 调用为 0。更完整的个案报告见 `docs/c2-success-seed-differential-analysis.md`。

## 5. C=1/2/4/8 逐步差分

| 配置 | Goal | 首次成功候选 | 候选 | Action |
|---|---:|---:|---:|---:|
| weak operators-only | 否 | - | 20 | 410 |
| realized-aware C=1 | 否 | - | 20 | 219 |
| realized-aware C=2 | 是 | 17 | 17 | 168 |
| realized-aware C=4 | 否 | - | 20 | 191 |
| realized-aware C=8 | 否 | - | 20 | 190 |
| planned-only C=4 | 否 | - | 20 | 191 |
| strong C=1 | 是 | 5 | 5 | 40 |

这是个案诊断，不能据此宣称 C=2 在总体上优于 C=1/4/8。

## 6. 首次关键分叉

Frontier parent 在 candidate 7 首先分叉，但实际 Action 尚未立刻不同：

- C=2 与 C=1 在 candidate 8、Plan Action 10 首次出现实际差异：C=2 Crash 节点 3，C=1 投递消息；
- C=2 与 C=4/C=8/planned C=4 在 candidate 9、Plan Action 10 首次出现实际差异：C=2 投递消息，其他配置 Crash 节点 1。

MessageID 只用于定位原始 Trace，不进入 Evidence 语义键。

## 7. 最小成功 Branch 骨架

C=2 的有效 parent 链为：

`candidate 10 → 11 → 12/13 → 15 → 16`

因果骨架是：

1. 形成唯一 Leader 和 TargetFollower；
2. TargetFollower 真实 Crash；
3. 目标离线时 active 节点开始选举；
4. active 集群 term 超过 crash 前目标 term；
5. TargetFollower Restart，但恢复尚未完成；
6. higher-term Vote 进入目标队列，形成 Commitment；
7. 投递该 Vote；
8. 目标节点产生预期 term/role 更新，Goal 成立。

candidate 15 和 16 是两个 `goal-b-higher-term-vote` 实例，不是两个不同 Branch。candidate 15 是 candidate 16 的直接 parent。它原计划为 Heartbeat，candidate 16 原计划为 MsgApp，实际都偏移到 Vote。

## 8. 单 Branch weak 可达性

能力诊断让每条 Branch 独占 C=1 和 20 个 candidate，使用 weak hints、staged Distance 和 prefix preservation。共 7 条 Branch × 10 seed = 70 个运行。最终修复后目录为
`/tmp/modelfuzz-ng-round6-single-branch-v3-20260728`；940 个 observed
Evidence 的首次 step 全部为非负值（范围 4～15）。

| Branch | Goal | Supported | Commitment | Full Realized | 最深 Waypoint |
|---|---:|---:|---:|---:|---:|
| A delayed-delivery | 0/10 | 6/10 | 3/10 | 0/10 | W2 |
| A drop-append | 0/10 | 10/10 | 0/10 | 0/10 | W2 |
| A snapshot-after-heal | 0/10 | 10/10 | 0/10 | 0/10 | W2 |
| A snapshot-failure-retry | 0/10 | 0/10 | 0/10 | 0/10 | W2 |
| B Heartbeat | 1/10 | 9/10 | 0/10 | 1/10 | W6 |
| B MsgApp | 1/10 | 10/10 | 0/10 | 1/10 | W6 |
| B Vote | 1/10 | 9/10 | 1/10 | 1/10 | W6 |

Heartbeat 和 MsgApp 的一次 Goal 到达实际都由 Vote deviation 完成。因此能力诊断只证明 Vote 路径在 weak 条件下偶尔可形成，没有证明 Heartbeat/MsgApp 自身形成。

## 9. 每条 Branch 的最深阶段

Goal A 的瓶颈仍在“隔离之后构造足够复制落后”：大量执行到 W2，但很少形成能决定 snapshot 路径的后续证据。failure-retry 连第一条所需 snapshot failure evidence 都没有稳定生成。

Goal B 能稳定 Crash 目标，但旧 weak mutation 经常没有在目标离线期间完成选举推进，或没有及时 Restart。只有 Vote Branch 在一条运行中同时达到 Commitment 和完整实现。

## 10. 预算稀释证据

C=2 个案中，C=4/C=8 在 20 个候选内轮换了更多浅 parent，而 C=2 连续选择了可推进链。单 Branch 实验又表明，即使独占预算，Goal A 仍为 0/10，Goal B 也只有实际 Vote 1/10。

所以当前同时存在两类障碍：

- **预算稀释**：多 Branch 轮换会减少同一因果链的连续尝试；
- **生成能力不足**：仅取消预算竞争仍不能稳定生成必要前提。

不能把所有 0/10 都解释成预算问题。

## 11. BranchEvidenceVector

版本固定为 `raft-branch-evidence-v1-prototype`。一个向量包含：

- Goal、Branch Template 和当前实例；
- 每个 Evidence 的状态、首次/末次观测 step、支持 Action、消息引用、模型事件和观测摘要；
- Supported/Necessary 数量；
- Commitment；
- RealizedDecidable、FullRealized、Contradicted、Invalidation；
- 下一关键事件是否已经可生成；
- 可保护的因果前缀边界；
- 不含具体身份和绝对数值的稳定语义键。

稳定键忽略 NodeID、MessageID、绝对 term/index、step 和 action 序号。具体引用仍保留在 artifact 中用于解释和 replay，但不决定两个 Evidence 是否属于同一语义。

正式实验审计发现过一次浅拷贝缺陷：构造稳定键时错误地清空了原向量中的具体支持信息。该批 `/tmp/modelfuzz-ng-round6-formal-v2-20260728` 被整体排除；修复后增加回归测试并从新目录重跑，避免选择性报告。

## 12. supported/committed/realized 区分

- **Planned**：只说明这次准备尝试哪条 Branch；
- **Supported**：至少一个必要的、前缀可观察的事实已经真实发生；
- **Committed**：该 Branch 的全部 commitment 必要条件已满足，下一关键事件已进入可生成阶段；
- **RealizedDecidable**：实际执行提供了足够信息，可以判断属于哪条 realized 路径或确定偏离；
- **Full Realized**：全部必要维度都有真实因果证据，且匹配某个冻结模板；
- **Contradicted**：已有证据与 planned 路径冲突；
- **Invalidated**：曾经成立的临时 Evidence 后来失效。本轮正式实现保留该状态和计数，但当前规则尚未产生可验证的 invalidation 实例，因此不能虚构非零结果。

## 13. Commitment Point

Commitment 不是 Goal，也不是“已经成功”。它表示当前前缀已经完成 Branch 的所有必要准备，使下一关键事件真正可生成。

例如 Goal B Vote 路径要求：目标已 Crash、active 选举已推进、term 已超过目标旧 term、目标已 Restart 且尚未恢复完成、higher-term Vote 已进入目标队列。最后的 Vote 投递和目标响应属于 Goal 完成证据，不属于 Commitment 前置条件。需要特别说明：第五轮冻结的 Full Realized 定义判断“实际 Branch 比较维度已经可决定”，因此可以在消息 pending、Goal 尚未完成时成立；本轮没有偷偷把它改成“Goal 已完成”。

## 14. Goal A Evidence Catalog

冻结的可行路径及关键证据为：

- `delayed-delivery`：目标已隔离、目标 MsgApp 被保留、旧 MsgApp 跨 Heal 仍存在；
- `drop-append`：目标已隔离、目标 MsgApp 真实 Drop、Drop 导致 lag 阶段变化；
- `snapshot-after-heal`：目标已隔离、Heal 时尚无目标 MsgSnap、MsgSnap 在 Heal 后生成；
- `snapshot-failure-retry`：第一次 MsgSnap 真实失败、协议随后生成 retry MsgSnap；
- `snapshot-before-heal` 仍在 Catalog 中，但当前配置判定为永久不可行，不参与可行 Branch 搜索。

## 15. Goal B Evidence Catalog

Heartbeat、MsgApp 和 Vote 三条路径共享：

- `target-crashed`；
- `active-election-started`；
- `active-term-advanced`；
- `target-restarted-incomplete`。

随后分别要求对应消息类别的：

- `higher-term-message-pending`：Commitment 所需；
- `higher-term-message-delivered`：Goal 完成所需的后续 Evidence。Catalog 仍保留
  `required_for_full_realization` 元数据以审计拟议的更严格口径，但本轮的
  `FullRealized` 布尔值继续沿用第五轮冻结的“Branch 比较维度已可决定”定义，
  所以两者必须分别报告，不能把 Full Realized 当作 Goal reach。

Pilot 证明，仅看 term 已增加过于粗糙；Crash 后 active 节点真实开始/推进选举是不可省略的因果准备，因此被补入冻结 Catalog。

## 16. Evidence causality

Evidence 分析只读取当前执行前缀：已执行 Action、Effect、已出现或保留的消息、model event、当前 Observation、网络/生命周期上下文和已经计算出的 Waypoint。它不读取未来 Trace、其他 candidate、最终 Campaign 成功率或历史自适应权重。

测试覆盖了“未来 Goal 成功不能反向产生早期 Evidence”、MessageID 不改变语义键、term/index 平移不改变语义、online/offline 重算一致等边界。

## 17. micro-progress 审计

旧 micro-progress 会把多个“与 Branch 有关”的事件都算作延长前缀的理由，其中既有必要准备，也有偶然或噪声事件。新 registry 显式登记每一种 micro-progress，Evidence 模式只允许 `necessary-only` 项目影响 prefix。

这不是删除诊断信息：incidental/noisy 事件仍可记录和统计，只是不再凭自身延长 Frontier 前缀。

## 18. necessary/incidental/noisy 分类

- **necessary**：后续路径成立必须发生，或使下一事件从不可生成变为可生成；
- **useful**：有明确帮助，但还不是必要条件；
- **incidental**：与目标同时出现，却没有证明改变可达性；
- **noisy**：只增加局部差异或重复事件。

Goal B 的 active-election-control/progress 在深度 Pilot 中被证明是 Crash 后推进 term 的必要准备，因此从一般 micro-progress 提升为 necessary Evidence。无关 Drop、重复失败投票、只改变队列数量的事件不会延长前缀。

## 19. Evidence Utility

Utility 使用固定的 3-candidate 窗口：

```text
某 Evidence 被观察后，在窗口内到达下一阶段的次数
------------------------------------------------
该 Evidence 被观察的总次数
```

同时记录 false-progress 和 sample-sufficient。它是离线诊断指标，不参与在线自适应学习。样本少于 10 时明确标记不足；因此单个高 Utility 不能直接证明因果，只能帮助发现值得进一步消融的 Evidence。

修复具体 Evidence 支持信息后的 5-seed Pilot 结果为：

| Goal | E0 Standard | E1 legacy micro | E2 Evidence |
|---|---:|---:|---:|
| A Goal / Commitment | 0/5 / 0/5 | 0/5 / 0/5 | 0/5 / 0/5 |
| B Goal / Commitment | 0/5 / 0/5 | 0/5 / 1/5 | 0/5 / 2/5 |
| A 总 Action | 579 | 647 | 633 |
| B 总 Action | 549 | 1118 | 611 |

这是 Pilot，不是正式性能结论。它支持继续用 30-candidate 正式实验检验 E2，但没有证明 Goal 到达率提高。

正式 Goal B 中 Utility 最高的项目也是 `active-term-advanced`：RR 的 Vote
为 7/16（0.438），Stage 为 9/24（0.375）。但相应 false-progress 分别仍有
9 和 15；`target-crashed` 的 Utility 多在 0.08～0.27。也就是说 term 推进比
单纯 Crash 更有信息，但仍远不是充分条件。大量 Supported 没有转化为 Goal，
正是本轮不进入在线学习或 Stall Detector 的理由。

## 20. Evidence-aware Frontier

选择顺序仍以 Goal progress 为主。只有进度相当时，才用 Necessary Evidence 数、Commitment 和 commitment diversity 区分 Seed。Progress guard 阻止明显更差的 Seed 仅凭 Evidence 垄断 Frontier；supported-only Seed 有独立槽位限制；planned seed 不占用 supported-only 槽。

Mutation 只把当前 Waypoint 推荐的动作类别权重乘以冻结常数 16。它仍随机选择可行动作，不指定 MessageID，不生成整条脚本，也不读取未来结果。

## 21. stage-budgeted 分配

阶段预算是确定性的固定配额，不做在线学习：

- initial quota：5；
- supported Evidence 追加：3；
- Commitment 追加：5；
- next Waypoint 追加：5；
- 每 Branch 总上限：20。

Branch 被 contradicted 后停止追加；总 candidate/action 上限继续由 Campaign 控制。ledger 记录 granted、used、action used、unused、reallocation、停止原因和是否在 Commitment 前耗尽。

## 22. round-robin 对比

round-robin 保持第五轮旧行为：可行 Branch 按固定顺序轮换；它不因为 Supported 或 Commitment 自动追加预算。stage-budgeted 先公平给 initial quota，再只向已经提供更强因果证据的 Branch 分配冻结追加配额。

两者的正式比较必须使用完全相同的 30 candidate / 4500 action 上限。单 Branch 独占预算和 C=2 个案不能代替这项公平比较。

## 23. 正式实验矩阵

修复后的正式矩阵为 2 个 Goal × 6 个方法 × 10 seed，共 120 个运行。每次最多 30 candidate、4500 action，strict TLC、prefix replay 和相同的 snapshot/crash/partition 配置保持一致。

| 方法 | 简称 | 说明 |
|---|---|---|
| M1 | weak operators-only | 无 Frontier 的 weak 操作基线 |
| M2 | Standard C=1 | weak Waypoint Frontier |
| M3 | Diversity C=4 | 第五轮 weak Branch Diversity |
| M4 | Evidence C=2 RR | Evidence-aware + round-robin |
| M5 | Evidence C=2 Stage | Evidence-aware + stage-budgeted |
| M6 | Strong C=1 | 人工强提示能力上界 |

为保证含义准确，M1/M2/M6 显式设置
`all_feasible_branches=false`；只有需要 Branch 规划的 M3/M4/M5 开启它。
实现同时修复了 manifest 布尔继承：现在“字段省略”才继承默认值，显式
`false` 不会再被默认 `true` 覆盖。

## 24. Goal reach

只采用修复后的 `formal-v4`：

| Goal | M1 | M2 | M3 | M4 RR | M5 Stage | M6 Strong |
|---|---:|---:|---:|---:|---:|---:|
| A Snapshot | 0/10 | 0/10 | 0/10 | 0/10 | 0/10 | 10/10 |
| B Restart | 0/10 | 7/10 | 1/10 | 7/10 | 3/10 | 10/10 |

Goal B 中，M4 与不使用 Branch/Evidence 的 M2 都是 7/10；M4 使用 221 个
candidate、1411 个 Action，M2 为 221/1398。当前没有 Evidence 提高 Goal
到达率或降低成本的证据。M5 只有 3/10，明显低于 RR 的 7/10。Goal A 仍完全
依赖 Strong 上界。

## 25. Commitment reach

Commitment 按运行和候选分别统计：

| Goal / 方法 | 至少一次 Supported 的 seed | 至少一次 Commitment 的 seed | Supported candidate | Committed candidate |
|---|---:|---:|---:|---:|
| A / M4 RR | 10/10 | 0/10 | 156 | 0 |
| A / M5 Stage | 10/10 | 0/10 | 165 | 0 |
| B / M4 RR | 10/10 | 5/10 | 125 | 5 |
| B / M5 Stage | 10/10 | 5/10 | 125 | 12 |

M5 在 Goal B 产生更多重复 Commitment instance，但到达过 Commitment 的 seed
仍是相同的 5/10，Goal 反而从 7/10 降为 3/10。只有 M5 记录到 3 次
Commitment 后窗口内的下一 Waypoint；M4 为 0。这个窗口指标没有转化成总体
Goal 收益，不能单独用来支持阶段预算。

## 26. Full Realized reach

Full Realized 继续按冻结定义统计。planned/actual 不一致时分别报告 planned
contradiction 和实际 realized template：

| Goal / 方法 | 至少一个 Full Realized 的 seed | RealizedDecidable candidate | Full Realized candidate | Contradicted candidate |
|---|---:|---:|---:|---:|
| A / M4 RR | 0/10 | 0 | 0 | 22 |
| A / M5 Stage | 0/10 | 0 | 0 | 4 |
| B / M4 RR | 7/10 | 26 | 18 | 22 |
| B / M5 Stage | 7/10 | 27 | 21 | 16 |

M5 的 Full Realized candidate 更多，但 Full Realized seed 仍为 7/10，Goal 只有
3/10。这个结果再次证明 Full Realized 是“Branch 比较维度可决定”，不等于
Goal 已完成，也不能用实例重复数替代 seed-level reach。

## 27. Failure-to-Form 分类

版本为 `raft-branch-formation-failure-v1-prototype`，支持：

`no-entry-state`、`binding-failed`、`prerequisite-not-generated`、`prerequisite-generated-not-selected`、`required-message-absent`、`required-message-blocked`、`required-message-dropped`、`wrong-message-class`、`election-not-completed`、`backup-log-not-fresh`、`lag-insufficient`、`compaction-boundary-not-crossed`、`heal-timing-missed`、`retry-not-triggered`、`prefix-not-preserved`、`evidence-invalidated`、`branch-contradicted`、`budget-diluted`、`budget-exhausted`、`currently-infeasible`、`permanently-infeasible`、`evaluator-undecidable`。

每条记录包含 primary cause、支持证据、first blocking step、failed stage、最深 Waypoint/Evidence Level 和建议的诊断类别。建议类别只用于人工分析，不自动生成修复代码。

正式 Evidence 运行的主要失败原因为：

- Goal A RR：`lag-insufficient` 147、`prerequisite-not-generated` 131、
  `branch-contradicted` 22；
- Goal A Stage：168、119、4；
- Goal B RR：`prerequisite-not-generated` 111、
  `election-not-completed` 83、`branch-contradicted` 9；
- Goal B Stage：112、82、7。

因此 Goal A 主要卡在构造足够复制落后，Goal B 主要卡在 Crash 后的选举准备；
不是简单扩大 Frontier 就能消除的障碍。

## 28. Snapshot mutant

Mutant 阶段只比较 Standard C=1、Evidence C=2 Stage 和 Strong C=1，并使用对应 control、相同 5 个配对 seed。缺陷为已有的 snapshot status invert，不新增或冒充生产 Bug。

| 方法 | Control Goal / Bug | Mutant Goal / Bug | Mutant candidate / Action |
|---|---:|---:|---:|
| Standard C=1 | 0/5 / 0/5 | 0/5 / 0/5 | 150 / 913 |
| Evidence Stage | 0/5 / 0/5 | 0/5 / 0/5 | 133 / 845 |
| Strong C=1 | 5/5 / 0/5 | 0/5 / 5/5 | 40 / 525 |

Snapshot 缺陷仍完全依赖 Strong 路径。Evidence 有 5/5 Supported，但 0/5
Commitment、0/5 Goal、0/5 Bug；它没有把局部证据转化成检错能力。

## 29. Restart mutant

第二个人工缺陷为 Restart lose HardState。仍分别记录 Goal、Evidence、Commitment、Full Realized 和 Bug；Bug 在 Commitment 前出现时，只归因到当时已有的证据级别。

| 方法 | Control Goal / Bug | Mutant Goal / Bug | Mutant candidate / Action |
|---|---:|---:|---:|
| Standard C=1 | 4/5 / 0/5 | 0/5 / 4/5 | 84 / 512 |
| Evidence Stage | 1/5 / 0/5 | 0/5 / 5/5 | 114 / 697 |
| Strong C=1 | 5/5 / 0/5 | 0/5 / 5/5 | 20 / 160 |

Evidence 比 Standard 多检出一个配对 seed，是积极信号；但只有 5 个 seed，且
Evidence 更贵，不能宣称稳定优势。五个 Evidence failure 全部发生在
Commitment 前：共 114 个 Planned、70 个 Supported、0 个 Committed、
0 个 Evidence Full Realized，`bug_before_commitment=5`。虽然旧 Branch
分析已可决定并记录 planned deviation，不能把这些 Bug 强行归因给完整 planned
Branch。

## 30. control false positive

control 的 Oracle/failure 必须单独统计。任何 control failure 都会阻止“mutant 检出能力”的简单结论，并需要先判断配置、Mapper、Oracle 或被测实现是否有问题。

六个 control campaign 共 30 个运行，Bug 为 0；Snapshot control 的 Goal
分别为 0/5、0/5、5/5，Restart control 为 4/5、1/5、5/5。control false
positive 为 0/30。control 的 Goal 差异说明方法到达能力不同，但不属于误报。

## 31. replay 和 ddmin

正式搜索继续验证 Frontier prefix replay。每个 mutant 至少选择一个可重复 failure，验证原始 replay，再按 normalized failure signature 执行 ddmin，并报告原始/最小 Action 数、重复验证次数和 one-minimal 状态。

- 全部正式实验 prefix replay：2160/2160；
- mutant 矩阵 prefix replay：1347/1347；
- Snapshot strong seed 5101：原始 Trace 17/17 matched；failure signature 为
  snapshot status mapping mismatch；Plan 15→13 Actions，47 次缩减尝试，
  最终 3/3 稳定，one-minimal=true；
- Restart Evidence seed 5103：原始 Trace 8/8 matched；signature 为
  `raft.basic:term_regressed`；Plan 8→4 Actions，27 次缩减尝试，最终 3/3
  稳定，one-minimal=true。

Replay 证明 concrete Trace 可重复；ddmin 重新执行 SUT、Mapper/Oracle 和 strict
TLC，证明同一 normalized failure signature 可由更短 Plan 稳定复现。

## 32. Facet、Goal、Evidence 和 Branch 的关系

- **Facet**：局部语义现象出现了什么；
- **Goal/Waypoint**：离人工定义的行为目标还有多远；
- **Evidence**：当前路径完成了哪些可验证的因果准备；
- **Branch**：到达同一 Goal 的完整路径属于哪一类。

它们互补而不是互相替代。Facet novelty 可以没有 Goal progress；Goal progress 可以暂时没有新 Evidence；Evidence 可以在不跨 Waypoint 时增强路径；只有完整证据才形成 Full Realized Branch。

## 33. 负结果

已经成立的负结果包括：

- 单 Branch 独占预算仍不能让 Goal A 成功；
- Heartbeat/MsgApp 的表面成功实际是 Vote deviation；
- Supported 很常见不代表 Commitment 常见；
- 仅增大 Frontier 容量没有单调收益；
- 旧 micro-progress 中存在不能证明因果价值的事件；
- 当前没有足够 invalidation 样本；
- Evidence Utility 的许多条目样本不足；
- C=2 唯一成功个案不能证明统计优势。
- Snapshot mutant 仍只有 Strong 能检出；
- Restart Evidence 的 5/5 相比 Standard 4/5 只是小样本积极信号，且失败均在
  Commitment 前。

## 34. 测试与静态检查

新增测试覆盖 schema、Evidence ID、稳定序列化、身份无关、term/index 平移、前缀因果、supported/committed/full 分离、contradicted/invalidated 分离、Frontier 容量与 progress guard、supported 槽、阶段配额、micro-progress 分类、mutation 只提高类别概率、原始 JSONL 重算等。

冻结后结果：

- `go test ./...`：通过；
- `go test -race ./...`：通过；
- `go vet ./...`：通过；
- `git diff --check`：通过。

测试使用本机已安装的 Go 1.26.4，并显式设置 `GOTOOLCHAIN=local`，没有下载
工具链。所有 120/60/70/30 组正式、mutant、单 Branch、Pilot status 均为
`completed`。

## 35. Artifact 与复现

主要输入 manifest：

- `examples/goal-benchmark-round6-c2-differential.json`；
- `examples/goal-benchmark-round6-single-branch-reachability.json`；
- `examples/goal-benchmark-round6-evidence-pilot.json`；
- `examples/goal-benchmark-round6-evidence-depth-pilot.json`；
- `examples/goal-benchmark-round6-formal.json`；
- `examples/goal-benchmark-round6-mutants.json`。

每个 Evidence 运行保存 catalog、raw JSONL、commitment、summary、formation failure、budget ledger/summary、micro-progress registry/utility 和 Frontier manifest。`goal-compare` 生成 `single-branch-reachability.csv`、`per-evidence-result.csv`、`per-branch-budget.csv` 和 figure-ready CSV。

接受的主要结果目录：

- C=2 raw/analysis：`/tmp/modelfuzz-ng-round6-c2-differential-raw-v2-20260728`
  和 `/tmp/modelfuzz-ng-round6-c2-analysis-v4-20260728`；
- 单 Branch：`/tmp/modelfuzz-ng-round6-single-branch-v3-20260728`；
- Pilot：`/tmp/modelfuzz-ng-round6-evidence-pilot-v4-20260728`；
- 正式：`/tmp/modelfuzz-ng-round6-formal-v4-20260728`。
- mutant、replay、ddmin：
  `/tmp/modelfuzz-ng-round6-mutants-v1-20260728`。

冻结输入 SHA-256：

- formal manifest：
  `5caa19d9def07937e4d71ea902d0f8a0aab7951b32c40d0baee06c8cc7217683`；
- mutant manifest：
  `559f0a80111932936a9fe6b37c937143792670c88655b5d4a3d6716630142664`；
- single-Branch manifest：
  `bad658be955cf7be1720e9933c70650407a5aaa916016cf2f1b5470581ec2649`；
- Pilot manifest：
  `54a5b0daa227e4b9d90a125b33a45c92ac353b89319c970ab1e208614c934d8b`；
- C=2 manifest：
  `d079d6d9c20a83092d14faf2d2712891749294d2eee93b6581912fab04699612`。

每个 output root 的 `benchmark-manifest.json`、`environment.json`、
`seed-manifest.json` 和 `benchmark-status.json` 保存了展开后的配置、版本、seed、
逐运行命令和完成状态。

被明确排除的目录：

- `/tmp/modelfuzz-ng-round6-c2-differential-raw-20260728`：布尔默认继承导致运行不完整；
- `/tmp/modelfuzz-ng-round6-formal-20260728`：启动后发现 manifest 未显式保存 priority multiplier；
- `/tmp/modelfuzz-ng-round6-formal-v2-20260728`：Evidence Stable Key 浅拷贝破坏具体支持信息。
- `/tmp/modelfuzz-ng-round6-formal-v3-20260728`：布尔默认合并使
  M1/M2/M6 无法关闭 `all_feasible_branches`，Strong Goal B 不再是已复现上界；
- `/tmp/modelfuzz-ng-round6-evidence-depth-pilot-v3-20260728`：运行于
  Stable Key 浅拷贝修复之前，只用于发现候选动作类别瓶颈，不作为最终 Pilot 数字。

## 36. 当前限制

当前只有两个人工 Goal、两个已知 mutant 和三节点 etcd-raft；Evidence Catalog 仍由人工定义。Utility 使用很短的固定窗口，不能替代因果消融。阶段配额是原型常数，不是通用公式。当前没有 membership、PreVote、CheckQuorum、真实磁盘或外部进程实验，也没有运行 LLM。

## 37. 下一轮建议

当前正式结果已经不满足方向 B 的必要条件：Goal A 没有 Commitment，Goal B
只有 5/10 seed 到达 Commitment，Stage 低于 RR，Evidence RR 又没有优于普通
Standard C=1。因此下一轮应选择**方向 A**，继续修正
Evidence/Mutation/Budget/Branch；mutant 结果只用于进一步确定优先修正哪一处。
本轮不会进入方向 B 或 C，也不会实现 LLM Planner。
