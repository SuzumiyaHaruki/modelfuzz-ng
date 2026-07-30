# Behavior Branch 与 Diversity-Aware Frontier：设计、实现和第五轮实验

## 1. 本轮问题

第三、四轮已经证明：人工 Behavior Goal、Waypoint、staged Distance 和可重放
Frontier 可以描述并搜索复杂 Raft 行为，但它们仍把“到达同一个 Waypoint 的不同原因”
放在一起。`W4=snapshot-required` 既可能来自保留旧 `MsgApp`，也可能来自丢弃复制
消息；`W5=higher-term-message-pending` 既可能绑定 `MsgVoteResp`，也可能绑定
`MsgApp` 或 `MsgHeartbeat`。只按 Goal progress 排序时，K>1 可能保存四条语义相同的
前缀，也可能丢掉尚未跨越 Goal Waypoint、但已经完成关键因果准备的前缀。

因此本轮没有直接加入 Stall Detector 或 LLM。若系统还不能回答“当前尝试的是哪条
语义路径、实际形成了哪条路径、哪条路径已经被充分尝试”，Stall 和 LLM 都缺少可靠的
结构化输入，很容易把普通随机停滞误当作推理问题。

## 2. 已实现能力与保持不变的边界

新增 schema 为：

```text
raft-behavior-branches-v1-prototype
```

新增代码集中在 `internal/goalsearch/branch.go` 和
`internal/goalsearch/branch_frontier.go`。原有 Goal Target Predicate、Waypoint、
Distance、Runtime、Mapper、TLC、Oracle、默认 Corpus 和普通 fuzz 均未修改。
`waypoint-frontier` 的历史“每个 Waypoint top-K”实现仍保留；公平实验显式启用独立的
固定总容量 Frontier。全部实验的 LLM 调用数为 0。

## 3. Behavior Branch

Behavior Branch 不是新的覆盖率，也不是第二套 Goal。它表示“在同一个 Goal 下，准备
尝试或已经真实形成的因果路径”。

### 3.1 Template

`BehaviorBranchTemplate` 至少保存：

- schema、Template ID、Goal ID、名称和说明；
- 配置/节点数适用条件；
- 结构化 Planned Dimensions；
- Binding Policy；
- 局部 Mutation Preferences；
- 禁止模式；
- 预期 Waypoint 路径；
- 可行性谓词；
- Realization Evidence；
- 稳定 key。

Template 是人工注册、类型安全和确定性排序的。它不包含节点 ID、最终运行状态或完整
成功 Plan。

### 3.2 Instance

`BehaviorBranchInstance` 对应一次实际 candidate，保存：

- Template、Branch Instance 和 Goal Instance 标识；
- Planned 与 Realized Signature；
- Goal 的实际 binding（只用于 artifact，不进入 Branch Signature）；
- Branch 状态、当前 Waypoint 和 Goal progress；
- 因果 evidence；
- deviation；
- feasibility；
- Frontier 引用和稳定 key。

### 3.3 Planned 与 Realized

`PlannedBranchSignature` 是 mutation 前选中的语义策略。`RealizedBranchSignature`
由实际 Trace 前缀重算。两者严格分开，不能把计划标签当成真实路径。

Signature 使用以下可选维度：

- `TargetSelectionClass`；
- `PartitionTopologyClass`；
- `LagConstructionClass`；
- `FaultDurationClass`；
- `TermAdvanceClass`；
- `HealTimingClass`；
- `SnapshotRouteClass`；
- `RecoveryRouteClass`；
- `KeyMessageClass`；
- `PreDeliverySequenceClass`。

key 不包含：

- NodeID；
- MessageID；
- 绝对 term、log index、snapshot index；
- seed、时间戳；
- Plan/Trace hash。

Realized 分类只扫描当前 Trace 前缀。最终成功不能反向改变早期分类；Heal 后出现的
`MsgSnap` 不会提前变成 before-heal；same-term 消息不会变成 higher-term evidence。
节点置换、MessageID 替换、term/index 整体平移均有单元测试。

### 3.4 Deviation

当某个已可判定维度与 Planned 不同，Instance 记录：

- 首次可判定 step；
- 对应 Waypoint；
- planned/realized 值；
- 当时已经存在的 evidence。

`short`/`medium` duration 是仍可能演化成 `long` 的前缀阶段，不会被过早判为偏离。
Seed 在 Realized-aware Frontier 中按真实分类保存，Planned 尝试仍计入原 Template。

## 4. Feasibility

状态包括：

```text
feasible
currently_infeasible
permanently_infeasible
not_decidable
violated
completed
```

静态不可行在执行前判定，不占 candidate/action 预算；暂时没有目标消息只记为
`currently_infeasible`。未知 Branch ID、跨 Goal Branch 和不支持的节点数会明确
拒绝。

Pilot 发现 `goal-a-snapshot-before-heal` 在当前“TargetFollower 与其余 quorum 完全
隔离”的拓扑下无法形成：Leader 要把 `next` 回退到压缩边界之前，需要来自目标的真实
reject response，但该 response 在 Heal 前同样被分区阻塞。因此该 Template 保留在
Catalog 和负结果中，但冻结为 `permanently_infeasible`，正式 `all-feasible` 不再给它
预算。这不是把失败分支改名为成功。

## 5. Binding 与 duration

当前冻结 Binding Policy 是 `least-advanced-eligible`，与 Goal W1 的确定性 binding
一致：最低 `last_index`，相同则使用稳定 tie-break。实际 NodeID 记录在 artifact，
但 Signature 只记录语义选择类别。

Goal A duration：

- `short`：已经结束分区但尚未到 W3；
- `medium`：已形成显著 lag，但尚未证明 Snapshot required；
- `long`：W4 已有真实 storage/progress/message 边界证据。

Goal B 通过 W3 evidence 的相对 term gap 和真实 action/effect 记录 term advance 原因，
不使用绝对 term。

## 6. 冻结 Branch Catalog

### 6.1 Goal A

| Template | 核心语义 | 冻结状态 |
| --- | --- | --- |
| `goal-a-delayed-delivery` | 保留跨 Heal 的旧 `MsgApp`，观察旧消息与 Snapshot 顺序 | feasible |
| `goal-a-drop-append` | 分区期间 Drop Leader→Target 的 `MsgApp` | feasible，但当前成功率低 |
| `goal-a-snapshot-after-heal` | Heal 时无 `MsgSnap`，Heal 后由 reject/backoff 触发 | feasible |
| `goal-a-snapshot-failure-retry` | 首个 `MsgSnap` Drop/失败，随后真实 heartbeat/retry | feasible |
| `goal-a-snapshot-before-heal` | 分区中先产生 `MsgSnap` | permanently infeasible |

Goal Target 仍是原来的 W7：真实 Snapshot 安装并推进安全恢复。

### 6.2 Goal B

| Template | Key message | pre-delivery 语义 |
| --- | --- | --- |
| `goal-b-higher-term-vote` | `MsgVote`/`MsgVoteResp` | immediate |
| `goal-b-higher-term-msgapp` | `MsgApp` | request-before-higher |
| `goal-b-higher-term-heartbeat` | `MsgHeartbeat` | tick-before-higher |

MsgApp/Heartbeat 路径暴露了一个真实搜索边界：备用 follower 若日志不够新，term 虽然
推进却无法当选，后续不会产生目标消息。因此 Branch micro-progress 会先投递
Leader↔备用 follower 的真实复制消息，再 crash Target、推进 term、完成 active
election、过滤非目标消息、restart，最后只以有限偏好产生所选消息类别。这不是完整
成功 Plan；每一步仍由实际 Observation 决定。

## 7. Mutation Preference

Weak Branch hints 只调整：

- Action 类别权重；
- 消息类别权重；
- 目标链路的语义角色；
- Heal 前后倾向；
- 是否保留旧消息；
- 所选 higher-term 消息类别。

Weak 不读取或绑定最终 MessageID。Strong 仍可使用当前 Goal 已经通过真实 evidence
绑定的精确消息，作为人工知识上界。Branch 不修改 SUT 状态，也不生成完整成功后缀。

固定 round-robin 在多个 Planned Branch 间分配 candidate；未实现 Bandit/RL。

## 8. Branch micro-progress

Pilot 的 Goal B 初版全部停在 W4。原因不是 action 不可执行，而是“丢掉一条非目标
消息”“完成 active election 的一次 vote 投递”不会跨越 Goal Waypoint。旧 Frontier
把 prefix 截回上一个 Goal progress，下一次又从丢消息之前开始。

本轮因此只在 Diversity Frontier 中增加 Branch micro-progress：

- 只有真实、已发生的 Branch evidence 才能延长 prefix；
- 标准 Frontier 仍只在 Goal progress 截断；
- prefix replay 仍验证完整 Concrete Trace；
- micro-progress 不改变 Goal distance 或 Target；
- 明显更差的 Goal progress 不能仅凭 evidence 永久占位。

这项结果说明 Goal 和 Branch 是互补信号：Goal 表示“离目标多远”，Branch 表示“到达
这里采用了什么因果准备”。

## 9. Diversity-Aware Frontier

### 9.1 固定总容量

新模式：

```text
diversity-aware-frontier
```

总容量为 C：

1. 先对每个已表示 Branch 保留一个最佳 Seed；
2. 若 Branch 数少于 C，再按可解释的 progress 排序填充；
3. 始终保证 `len(seeds) <= C`；
4. 不使用“Branch 数 × 每 Branch K”。

普通公平对照使用独立 `CapacityFrontier`，同样是全局 C；旧
`NewFrontier(topK)` 的 per-waypoint 语义不变。

### 9.2 排序和去重

Branch 内顺序：

1. Completed Waypoint；
2. staged Distance；
3. Evidence strength；
4. prefix length；
5. stable key。

Branch 间优先真实 Realized Branch；尚未可判定时用 Planned Branch 分开，避免早期
全部塌缩到 unknown。选择时只允许最多落后最佳值一个 completed waypoint 的 Seed
参与轮转，这是明确的 progress guard。

语义去重使用 binding role、相对 term/log/commit、网络阻塞、消息类别和数量 bucket，
同时保留 from-role/to-role 方向；不使用具体节点或消息身份。

## 10. CLI、Manifest 和 Artifact

主要开关：

```text
-branch-templates a,b,c
-all-feasible-branches
-total-frontier-capacity N
-per-branch-minimum-capacity N
-branch-awareness planned-only|realized-aware
-branch-dimension-ablation none|key-message|heal-timing|lag-construction|term-advance
-branch-budget-allocation round-robin
```

每个 run 新增：

- `branch-catalog.json`；
- `branch-settings.json`；
- `branch-feasibility.json`；
- `branch-instances.jsonl`；
- `branch-progress.jsonl`；
- `branch-frontier-manifest.json`；
- `planned-realized-mapping.json`；
- `branch-summary.json`。

`branch-summary.json` 从 `branch-progress.jsonl` 确定性重算，而不是只信任内存聚合。
Benchmark 新增 `per-seed-branches.csv` 和 `per-branch-bug-detection.csv`。

## 11. Pilot 与冻结 Catalog

最终 Pilot 使用本地进程内 etcd-raft、strict storage-snapshot TLC、3 节点以及
`5201..5203`。它只验证 Branch 在 strong 上界下是否可执行，不作为正式成功率。

| Branch | Goal 达成 | 完整 Realized | 平均 candidate / Action | 结论 |
| --- | ---: | ---: | ---: | --- |
| Delayed-Delivery | 3/3 | 3 | 8 / 105 | 可执行 |
| Drop-Append | 0/3 | 0 | 18 / 269 | 能尝试，但当前恢复控制不足 |
| Snapshot-After-Heal | 3/3 | 3 | 8 / 105 | 可执行 |
| Snapshot-Failure-Retry | 0/3 | 0 | 18 / 275 | 当前未形成完整 retry 路径 |
| Higher-Term Heartbeat | 3/3 | 3 | 11 / 146 | 可执行 |
| Higher-Term MsgApp | 3/3 | 3 | 9 / 111 | 可执行，但中间尝试有 6 次部分偏离 |
| Higher-Term Vote | 3/3 | 3 | 9 / 111 | 可执行 |

所有 Pilot 的 online/offline mismatch 为 0，prefix replay 全成功。
`Snapshot-Before-Heal` 在早期 Pilot 中是 0/3；协议分析表明当前完全隔离拓扑无法在
Heal 前收到 Target reject 并回退 `next`，因此冻结为 `permanently_infeasible`。
它保留在 Catalog 和 feasibility artifact 中，但从最终可执行 Pilot manifest 移除，
不再消耗 candidate/action 预算。

## 12. 正式方法、Seed 和预算

正式稳定性 seed 固定为 `4101..4110`：

| 方法 | Hint | Branch | Frontier |
| --- | --- | --- | --- |
| M0 | none | 无 | 无 |
| M1 | weak | round-robin | 无 |
| M2 | weak | round-robin | Standard fixed total C=1 |
| M3 | weak | round-robin | Standard fixed total C=4 |
| M4 | weak | round-robin | Diversity fixed total C=4 |
| M5 | strong | 无 | Standard fixed total C=1 |

Goal A 运行 M0–M5，Goal B 运行 M1–M5，共 110 次。每次上限为 20 candidates、
3000 累计 concrete Actions、180 Plan actions；到达目标即停止。M5 不携带 Branch，
是上一轮人工知识上界，不把 strong 成功后缀泄漏给 M4。

## 13. Goal reach、成本和统计边界

| Goal | 方法 | 达成 | 平均 candidate | 平均 Action | 平均墙钟 ms |
| --- | --- | ---: | ---: | ---: | ---: |
| A | M0 Unguided | 0/10 | 20.0 | 350.5 | 1978.4 |
| A | M1 Weak operators | 0/10 | 20.0 | 399.6 | 2502.0 |
| A | M2 Standard C=1 | 0/10 | 20.0 | 122.7 | 1204.2 |
| A | M3 Standard C=4 | 0/10 | 20.0 | 115.6 | 1134.8 |
| A | M4 Diversity C=4 | 0/10 | 20.0 | 121.5 | 1345.6 |
| A | M5 Strong C=1 | 10/10 | 8.0 | 105.0 | 1232.8 |
| B | M1 Weak operators | 1/10 | 20.0 | 368.7 | 2331.9 |
| B | M2 Standard C=1 | 0/10 | 20.0 | 120.3 | 1253.1 |
| B | M3 Standard C=4 | 0/10 | 20.0 | 112.3 | 1141.8 |
| B | M4 Diversity C=4 | 0/10 | 20.0 | 201.9 | 2430.8 |
| B | M5 Strong C=1 | 10/10 | 5.0 | 40.0 | 457.9 |

0/10 的 Wilson 95% 上界约为 27.8%，1/10 区间约为 1.8%–40.4%，10/10 下界约为
72.2%。样本不足以宣称统计显著。能安全写出的结论是：strong 上界保持完整能力，
当前 M4 没有把路径多样性转化为 weak Goal reach；Goal B 的 M1 单次成功也未被 M4
稳定复现。

## 14. Planned、Matched、Deviation 与完整 Realized

本轮最终修正了一个重要统计口径：

- `MatchedTemplateID`：当前已知维度“看起来像”哪个模板；
- `Deviation`：一个已知维度已经足以证明与 Planned 冲突；
- `RealizedDecidable`：所有 Planned 比较维度均已有因果 evidence；
- 只有 `RealizedDecidable=true` 才进入完整 Realized Branch count。

因此“部分匹配”或“提前发现偏离”都不能冒充完整实际路径。正式 M4 中：

| Goal | Planned 模板 | 尝试 | 完整 Realized | 最深 W | retained | evicted |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| A | Delayed-Delivery | 50 | 0 | W2 | 21 | 11 |
| A | Drop-Append | 50 | 0 | W2 | 26 | 23 |
| A | Snapshot-After-Heal | 50 | 0 | W2 | 27 | 27 |
| A | Snapshot-Failure-Retry | 50 | 0 | W2 | 24 | 24 |
| B | Heartbeat | 70 | 0 | W3 | 41 | 32 |
| B | MsgApp | 70 | 0 | W2 | 36 | 26 |
| B | Vote | 60 | 0 | W2 | 40 | 29 |

Goal A 每个 run 实际选择 4 个可行 Planned 模板，并另外在 feasibility 中记录 1 个永久
不可行模板；Goal B 每个 run 选择 3 个。聚合报告中的 Planned count 50/30 是逐 seed
求和，不是 Catalog 有 50/30 个模板。正式 M4 的 400 次尝试没有完整 Realized Branch，
所以 planned-realized agreement rate 不应被写成 0% 的“失败率”，而应写成“无可判定
分母”。这正是下一轮必须修正的 evidence/生成边界。

## 15. Trace、Branch 与 Frontier 多样性

| Campaign | exact Trace | 相对语义 Trace | Goal progress path | 最终 queue shape |
| --- | ---: | ---: | ---: | ---: |
| A Standard C=4 | 10 | 8 | 10 | 9 |
| A Diversity C=4 | 10 | 9 | 10 | 9 |
| B Standard C=4 | 10 | 6 | 9 | 6 |
| B Diversity C=4 | 10 | 8 | 10 | 7 |
| A Strong C=1 | 10 | 1 | 1 | 1 |
| B Strong C=1 | 10 | 1 | 1 | 1 |

Diversity 确实增加了相对语义 Trace/queue shape，但没有产生完整 Realized Branch，也没有
贡献 Goal。M4-A 的 Frontier 为 inserted/replaced/evicted=`122/3/85`，M4-B 为
`113/14/87`；这说明额外候选大量参与容量竞争，不能只报告“保留了不同 Planned
标签”。

Strong 的 10 条 exact Trace 各不相同，但两个 Goal 都只有 1 条相对语义 Trace、1 条
progress path 和 1 种最终 queue shape，路径塌缩没有被 M4 的 weak 搜索缓解。

## 16. 固定容量、Planned/Realized awareness 与维度消融

Goal B 消融结果：

| 配置 | Goal | 完整 Realized | 平均 Action | inserted / replaced / evicted |
| --- | ---: | ---: | ---: | ---: |
| realized-aware C=1 | 0/10 | 1 | 233.1 | 178 / 0 / 168 |
| realized-aware C=2 | 1/10 | 2 | 222.8 | 151 / 3 / 134 |
| realized-aware C=4 | 0/10 | 0 | 201.9 | 113 / 14 / 87 |
| realized-aware C=8 | 0/10 | 0 | 195.3 | 105 / 16 / 42 |
| planned-only C=4 | 0/10 | 0 | 201.9 | 113 / 14 / 87 |
| C=4，移除 key-message | 0/10 | 0 | 212.1 | 119 / 12 / 91 |

容量 2 的单次成功伴随 2 个完整 Realized Branch，是值得下一轮单独分析的前缀，但容量
1/4/8 都没有成功，不能推出容量 2 最优。C=4 下 planned-only 与 realized-aware 完全
相同，是因为没有 candidate 到达完整 Realized 判定点；realized-aware 实际退化为
undecided Planned 隔离。移除 key-message 没有改善结果。

## 17. Directed reference 对应的 Branch

第四轮 seed 4101 的 directed Snapshot Trace 在 step 17 执行 Heal，step 22 才因真实
消息处理产生 `MsgSnap`，所以它对应 `Snapshot-After-Heal`。该判断来自已保存 Trace，
不是用最终 Goal 反推早期 Branch。Directed reference 一次到达 Goal，但没有进入
Frontier Corpus，也没有用于自动补全任何成功后缀。正式 M4 未产生 directed 路径之外
的成功 Branch。

## 18. Mutant、control 和 per-Branch 缺陷归因

Mutant 使用 seed `4101..4105`、相同预算、strict TLC 和配对 control：

| Goal / 方法 | control failure | mutant detection | 平均 Action-to-failure |
| --- | ---: | ---: | ---: |
| Snapshot Weak Standard C=1 | 0/5 | 0/5 | — |
| Snapshot Weak Standard C=4 | 0/5 | 0/5 | — |
| Snapshot Weak Diversity C=4 | 0/5 | 0/5 | — |
| Snapshot Strong C=1 | 0/5 | 5/5 | 105 |
| Restart Weak Standard C=1 | 0/5 | 1/5 | 123 |
| Restart Weak Standard C=4 | 0/5 | 0/5 | — |
| Restart Weak Diversity C=4 | 0/5 | 3/5 | 194.7 |
| Restart Strong C=1 | 0/5 | 5/5 | 32 |

所有 control false-positive rate 为 0。Snapshot mutant 仍需要 strong 路径；Branch
机制没有保持上一轮 strong Snapshot 检出能力，因此不能替代 strong 上界。Restart
mutant 中 M4 的 3/5 高于两个 weak standard 对照，但样本只有 5，且平均首次失败 Action
高于 Standard C=1 的唯一成功，不能宣称显著提升。

M4-Restart 的三次 failure 都发生在 Goal 前的 W3：

- seed 4102、4103：Planned Vote，各贡献 1 次；
- seed 4104：Planned Heartbeat，贡献 1 次；
- 当时完整 Realized Branch 均不可判定，因此 per-Realized bug detection 为空；
- artifact 仍保存 partial matched template 和 `realized_decidable=false`，但不强行归因。

这连接了“哪条计划路径触发错误”，同时避免把部分证据包装成完整实际路径。

## 19. Replay、ddmin 和失败签名

| 失败 | 原 Trace replay | Plan 缩减 | 尝试 / cache hit | 最终验证 | one-minimal |
| --- | ---: | ---: | ---: | ---: | ---: |
| Snapshot status invert | 17/17 step | 15→13 | 46 / 14 | 3/3 | 是 |
| Restart lose HardState | 7/7 step | 7→4 | 22 / 5 | 3/3 | 是 |

Snapshot 保持规范化 `mapping_failed / snapshot status progress mismatch`，Restart
保持 `oracle_failed / raft.basic:term_regressed`。ddmin 前各验证 2 次，均未触及
200 次尝试上限。

## 20. Facet、Goal 与 Branch 的关系

三者回答不同问题：

- Facet：执行覆盖了哪些协议语义切面；
- Goal/Waypoint/Distance：离人工行为目标还有多远；
- Branch：以什么因果路径到达当前前缀。

M4-A 有 45 次 new Facet without Goal progress，M4-B 有 68 次；反向的 Goal progress
without new Facet 分别为 1 和 6。由于正式 M4 没有完整新 Realized Branch，
`new Branch without new Facet=0`，而 `new Facet without new Branch` 分别为 69 和
83。结果证明 Facet novelty、Goal progress 与完整 Branch realization 不是同一个信号，
也说明当前 Branch 不是覆盖广度指标。

## 21. 负结果、排除项与不能推出的结论

必须保留的负结果：

- M4 在两个 Goal 都是 0/10；
- M4 的 400 次正式尝试没有完整 Realized Branch；
- Goal A 四个可行 Planned Branch 都只到 W2；
- Goal B 的 Planned Heartbeat 最深到 W3，MsgApp/Vote 只到 W2；
- C=4 planned-only 与 realized-aware 相同；
- 增加相对语义 Trace 没有带来 Goal；
- Snapshot-Failure-Retry 和 Drop-Append Pilot 都是 0/3；
- weak Diversity 不能检出 Snapshot mutant；
- strong 轨迹仍塌缩为单一相对语义路径。

开发过程中明确排除：

- JSON `from/to` 重复 tag 之前的稳定性目录；
- `snapshot_threshold=2` 覆盖默认值的两轮 mutant 目录；
- 跨 Goal 无关维度进入 Realized key 的目录；
- 用 feasibility/partial deviation 近似完整 Realized 的目录；
- 被中断、非最终二进制或仅用于定位实现问题的目录。

不能推出：

- Branch 对所有 Raft/共识系统无效；
- 容量 2 是最优值；
- Restart 3/5 已具有统计显著性；
- strong 10/10 等于完整状态覆盖；
- 人工 mutant 是新的生产 bug；
- 当前结果证明 LLM 有效或无效。

## 22. Artifact 与复现

最终输入：

- `examples/goal-benchmark-branches-pilot.json`
- `examples/goal-benchmark-branch-diversity-stability.json`
- `examples/goal-benchmark-branch-diversity-ablations.json`
- `examples/goal-benchmark-branch-diversity-mutants.json`

最终原始输出：

- `/tmp/modelfuzz-ng-round5-branches-pilot-final-v7-20260728`
- `/tmp/modelfuzz-ng-round5-branch-diversity-stability-final-v7-20260728`
- `/tmp/modelfuzz-ng-round5-branch-diversity-ablations-final-v7-20260728`
- `/tmp/modelfuzz-ng-round5-branch-diversity-mutants-final-v7-20260728`

每个正式根目录含 manifest、status、environment、seed manifest/diversity、
comparison summary、figure-ready CSV、per-seed Branch CSV、per-Branch bug CSV。
每个 seed 的 `branch-progress.jsonl` 是最终 Branch 汇总的原始来源；
`branch-summary.json` 已做两次确定性重算测试。完整实际命令保存在
`benchmark-status.json`。Replay/ddmin 位于 mutant 根目录的 `failure-analysis/`。

复现：

```bash
go run ./cmd/modelfuzz-ng goal-benchmark \
  -manifest examples/goal-benchmark-branch-diversity-stability.json \
  -output /tmp/round5-stability

go run ./cmd/modelfuzz-ng goal-benchmark \
  -manifest examples/goal-benchmark-branch-diversity-ablations.json \
  -output /tmp/round5-ablations

go run ./cmd/modelfuzz-ng goal-benchmark \
  -manifest examples/goal-benchmark-branch-diversity-mutants.json \
  -output /tmp/round5-mutants
```

## 23. 测试与静态检查

最终通过：

- `go test ./...`；
- `go test -race ./...`；
- `go vet ./...`；
- `git diff --check`；
- Branch schema/唯一 ID/稳定序列化；
- NodeID、MessageID、term/index 平移不变性；
- Goal A/B 维度交叉泄漏测试；
- same-term 与 higher-term 消息分类；
- Planned/Realized 分离和因果 deviation；
- permanently infeasible 不消耗预算；
- fixed-total control 不改变旧 per-waypoint Frontier；
- Diversity capacity、planned-only 和淘汰计数；
- online/offline 与 prefix replay；
- benchmark 恢复跳过、CSV 维度和汇总重算；
- control/mutant 分离以及 pre-decidable bug 的 Planned 归因。

## 24. 当前限制

- 当前只有两个 Raft Goal，不能外推所有共识系统；
- Binding Policy 只有 `least-advanced-eligible`；
- Branch Catalog 仍由人工定义；
- weak preference 尚不能可靠完成 Branch evidence；
- Realized Branch 要求完整维度，当前可能过严，也可能是 mutation 没有形成路径；
- 未实现 Stall、Bandit、RL 或 LLM Planner；
- 未运行 5 节点、其他 tick、更多 snapshot 阈值和外部历史 bug；
- Branch count 不是有已知总分母的覆盖率。

## 25. 下一轮选择：方向 A

选择方向 A，不进入 B，更不进入 C。直接证据是：

- 两个 M4 都是 0/10；
- 正式 M4 没有完整 Realized Branch；
- realized-aware C=4 退化成 Planned/undecided 隔离；
- 增加语义 Trace 和 queue shape 没有贡献 Goal；
- strong 路径塌缩未缓解；
- 只有 C=2 的单 seed 成功，不能构成可靠搜索；
- Restart mutant 3/5 是积极信号，但发生在完整 Realized 判定之前；
- Snapshot mutant 能力仍完全依赖 strong 上界。

下一轮应先分析 C=2 成功 seed 与 M4 失败前缀，修正 Branch evidence 完整性、
weak mutation 和 progress/dedup 关系，再决定是否具备 Goal-local Stall Detector 的
输入条件。本轮到此停止，不自动实现下一方向。
