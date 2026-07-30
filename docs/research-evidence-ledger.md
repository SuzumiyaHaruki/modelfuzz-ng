# 研究证据账本：关键实验、可支持结论与待排除解释

> 用途：集中记录会影响研究结论、解释设计选择、排除其他解释，或决定下一阶段方向的实验。
> 更新日期：2026-07-29
> 适用范围：`modelfuzz-ng-partition` 中的 Raft 状态覆盖、Behavior Goal、Waypoint
> Frontier、缺陷检测和未来 LLM 辅助测试研究。

## 1. 为什么需要这份账本

普通开发日志回答“做了什么”，这份文档回答：

1. 哪条研究结论由哪组实验支持；
2. 实验排除了哪些其他解释；
3. 哪些结果只能算小规模现象，不能写成普遍结论；
4. 哪些失败或无收益结果必须保留；
5. 未来论文中的主张还缺什么证据。

本文件不替代各实验的完整报告。完整配置、命令、逐运行数字和 artifact 仍以链接的原始
报告为准。本文件只负责组织证据链。

## 2. 状态、证据等级与写作规则

### 2.1 状态

| 标记 | 含义 |
|---|---|
| 已完成 | 已运行真实实验并保存结果 |
| 初步 | 有真实结果，但 seed、配置或对照不足 |
| 负结果 | 没有收益、没有成功或推翻了原假设；仍属于重要证据 |
| 待实验 | 只有问题和实验设计，不能写成结果 |
| 被替代 | 早期口径已被更严格实验替代，仅保留历史解释 |

### 2.2 证据等级

| 等级 | 最低要求 | 可以支持的表达 |
|---|---|---|
| A | 多 seed、匹配对照、预算可比、结果可重算/重放 | “在给定实验范围内稳定观察到……” |
| B | 多条真实轨迹或多配置验证，但缺少完整消融 | “结果支持……，但尚不能排除……” |
| C | 小规模 smoke、单配置或高度确定性 operator | “初步观察到……，需要扩大验证” |
| D | 仅设计、单元测试或理论分析 | 只能说明“已定义/可执行”，不能说明效果 |

等级不是论文重要性。一个核心方法在早期也可能只有 C 级证据。

### 2.3 论文表述纪律

允许：

- “在 3 个确定性 seed 的小规模实验中，Frontier 对 Goal A 为 3/3，而
  Goal-aware-only 为 0/3。”
- “v2 在 24-run 随机样本中比 v1 减少 25.75%，但 24/24 运行仍产生新 v2 状态。”
- “三类人工 mutant 均能被现有检测链发现，且主要检测层不同。”

不允许：

- 把“达到人工 Goal”直接写成“发现 bug”；
- 把 3/3 写成“Frontier 普遍优于其他方法”；
- 把状态数更少写成“覆盖更准确”；
- 把 0 次失败写成“系统无 bug”；
- 把无 TLC 的运行写成 TLA+ 状态覆盖；
- 把未来计划或单元测试写成已经获得的实验结果。

## 3. 当前证据总览

| ID | 内容 | 状态/等级 | 当前能支持的结论 | 论文价值 |
|---|---|---|---|---|
| COV-01 | Raw/v1 → v2 的粒度演化 | 已完成 / B | v1 确实较细；v2 更粗但随机空间仍未饱和 | 解释为什么不直接使用完整状态数 |
| COV-02 | 字段消融与状态增长来源 | 已完成 / B | 状态增长主要来自 node shape 与 lag 组合积 | 证明 Facet 不是凭直觉拆分 |
| COV-03 | Facet 与 interaction 饱和行为 | 已完成 / B | 不同行为维度的增长速度不同，完整状态掩盖来源 | 覆盖设计的核心证据 |
| CORPUS-01 | semantic on/off 1000-run A/B | 已完成、负结果 / A | 当前门槛只影响 0.2% 运行，不能解释明显效率收益 | 排除默认 Corpus 门槛的影响 |
| GOAL-01 | Goal/Waypoint 定义与因果判定 | 已完成 / D+B | 方法已实现；195 条 online/offline 一致 | 核心方法正确性 |
| REPLAY-01 | Prefix replay 与 Plan-prefix 再执行 | 已完成 / A | 第四轮稳定性 721/721，mutant 480/480，前缀执行不一致为 0 | 证明 Frontier 可可靠复用 |
| METHOD-01 | 普通、Goal-aware、Frontier 三方法 | 已完成 / A | Goal A Frontier 10/10 对 operators 0/10；Goal B strong 都为 10/10 | 方法收益归因 |
| ABLATION-01 | Hint、top-K、prefix、Distance 消融 | 已完成 / A | prefix 与 staged Distance 对 Goal A 必要；K=1 当前成本最低 | 解释 Frontier 收益来源 |
| COVERAGE-01 | Goal progress 与 Facet novelty | 已完成 / B | 353 次新 Facet 无 Goal 进展；6 次新 Waypoint 无新 Facet | 证明两种信号互补 |
| SEED-01 | 当前 seed 规模与调度多样性 | 负结果 / B | 已扩到 10 seed，但 strong 方法仍只有一种相对语义轨迹 | 排除“seed 数等于调度多样性” |
| BUG-01 | 三类人工 mutant 检测 | 已完成 / A | 当前测试系统具备真实缺陷检测链 | 连接“到达场景”和“发现错误” |
| BUG-GOAL-01 | Goal 搜索匹配 mutant | 已完成 / A | Goal A Frontier 10/10、其他方法 0/10；Goal B guided 10/10 | 证明目标搜索具有测试价值 |
| RANDOM-01 | 大规模随机基线 | 已完成 / A | 系统能长时间稳定执行；0 failure 不等于无 bug | 可靠性与随机基线 |
| DET-01 | Replay、checkpoint 与历史 seed 复现 | 已完成 / A | 轨迹与恢复路径可重复 | 保证实验可信 |
| PERF-01 | strict TLC 性能归因 | 已完成 / A | 主要瓶颈曾是 Action 查找，不是 invariant cache | 解释实现与实验成本 |
| DIRECTED-01 | Snapshot/Partition 定向上界 | 已完成 / B | 目标场景本身可稳定构造 | Goal A 参考上界 |
| LLM-01 | LLM 对方法的独立贡献 | 待实验 | 当前无证据 | 未来论文核心 |

## 4. 已完成的关键证据

### COV-01：从 v1 完整状态到 v2 粗粒度状态

**研究问题**

完整 TLA+ 语义状态是否因为 term、index、节点身份和内部记账差异而增长过快？

**实验**

- 24 条随机轨迹：2,952 次 model-state visit；
- 8 条 Snapshot/Partition 定向轨迹：232 次 model-state visit；
- 都是真实 etcd-raft、storage-snapshot TLA+ profile 和 strict TLC；
- v1/v2 从同一份 `model-states.json` 离线重算。

**结果**

| 数据 | v1 distinct | v2 distinct | 减少比例 | 有新 v2 的运行 |
|---|---:|---:|---:|---:|
| Random 24-run | 1,491 | 1,107 | 25.75% | 24/24 |
| Snapshot/Partition 8-run | 64 | 22 | 65.62% | 1/8 |

Random 的 v2 Q4/Q1 从 v1 的 0.821 降到 0.543，但 24/24 仍然产生新 v2。

**支持的结论**

- v1 确实会把一部分数值和身份差异保留下来；
- v2 能合并重复的高层 Snapshot 行为；
- 仅仅继续把所有语义字段拼成一个完整状态，仍不能得到容易解释的覆盖终点。

**尚未排除**

- v1 的额外区分中可能包含发现特定 bug 所需的信息；
- 24 条随机轨迹太少，不能据此估计长期饱和速度；
- “状态数减少”不能证明“评价标准更好”。

**论文用途**

适合用一段话和一张演化图说明 `Raw/v1 → v2 → Facet` 的动机。

**来源**

- [Raft Semantic Coverage v2 Prototype](semantic-coverage-v2-prototype.md)
- Artifact：`/tmp/modelfuzz-ng-coverage-v2-real-20260728`

---

### COV-02：字段消融与状态爆炸来源

**研究问题**

v2 的 1,110 个完整状态到底来自哪些字段？Facet 拆分是否只是人为直觉？

**实验**

对 32 份完整 artifact、4,255 个 CoverageFrame 做单字段消融、字段组消融和条件分裂。
三次离线报告均为 `deterministic=true`。

**结果**

| 删除内容 | 删除后 distinct | 对完整基数的贡献 |
|---|---:|---:|
| `canonical_node_shapes` | 366 | 67.03% |
| lag 组 | 484 | 56.40% |
| node `commit_lag` | 765 | 31.08% |
| node `applied_lag` | 808 | 27.21% |
| Snapshot 组 | 837 | 24.59% |
| log/catch-up 组 | 907 | 18.29% |
| voting 组 | 1,036 | 6.67% |

`canonical_node_shapes` 自身有 1,110 种，与完整 v2 distinct 完全相同。多个顶层字段
单独删除贡献为 0%，因为相同事实已经被 node shapes 重复表达。

**支持的结论**

- 状态增长主要来自每个节点的 lag、日志、Snapshot 形状组合，而不是某一个高基数字段；
- 即使每个子字段只有 2–4 个 bucket，组合后仍会形成大量完整状态；
- Election、Replication、Snapshot、Recovery、Network 分开统计有数据依据。

**排除的解释**

- 不能把爆炸简单归因于绝对 term；
- 不能只删除一两个顶层字段就解决问题；
- Facet 不是为了制造新名词而任意切分。

**论文用途**

适合在评价标准设计部分展开，是解释 Facet 设计合理性的主要实验证据。

**来源**

- [Raft 语义覆盖分解与状态增长归因](semantic-coverage-factorization.md)
- Artifact：`/tmp/modelfuzz-ng-factorization-combined-final.json`

---

### COV-03：Facet 的增长和饱和位置

**研究问题**

分开后的 Facet 能否说明“新覆盖来自哪里”，而不只是给出另一个数字？

**结果**

Random 24-run：

| Facet | distinct | 有新增的运行 | 最后新增运行 | Q4/Q1 |
|---|---:|---:|---:|---:|
| Election | 63 | 15/24 | 24 | 0.103 |
| Replication | 228 | 23/24 | 24 | 0.139 |
| Snapshot | 33 | 11/24 | 24 | 0.037 |
| Recovery | 28 | 9/24 | 19 | 0.105 |
| Network | 28 | 14/24 | 20 | 0.143 |

定向 Snapshot/Partition 8-run 中，五个 Facet 的全部新值都来自第一条执行，其余执行
重复同一高层场景。

**支持的结论**

- Snapshot 在该随机样本中最接近饱和；
- Replication 仍持续增长，不能把“Facet 数量较少”误写成“已经覆盖充分”；
- 定向轨迹可以重复实现同一行为，而完整状态数仍可能因细节变化增长；
- Facet 可以告诉我们新增来自复制、网络还是恢复。

**论文用途**

主文可简述，详细表格适合评估章节或附录。

---

### CORPUS-01：语义准入门槛是否已经带来效率收益

**研究问题**

已有 semantic coverage gate 是否已经能够解释后续方法的覆盖或效率差异？

**实验**

同 seed、1000-run A/B，只切换 `semantic-coverage`，raw threshold 都为 25。

**结果**

- semantic on/off 都是 1000/1000 成功；
- Corpus 都是 393；
- semantic on 仅额外拒绝 2 条，即 0.2%；
- novelty/100 Action 为 22.2227 对 22.2268，差异为噪声级；
- 前 402 条 Plan/Trace 一致，首次分歧的候选新增 raw state，但没有新增语义状态/转移。

**支持的结论**

- gate 的实现和原因码正确；
- 当前配置下 raw threshold 仍是主要准入条件；
- 不能把当前系统的显著效率收益归因于 semantic gate。

**负结果价值**

这是一项应保留的“机制有效但当前收益很小”的结果，能防止论文把所有改进都归因于
语义覆盖。

**来源**

- [feedback-tuning-v7：1000-run 语义准入 A/B](experiments/feedback-tuning-v7-20260722.md)

---

### GOAL-01：Goal/Waypoint 定义与因果判定

**研究问题**

人工 Goal 能否在执行过程中判断，而不从最终状态反推早期事件？

**已实现与验证**

- 两个固定 Goal：Snapshot catch-up、Restart/higher-term；
- State 与 Event/Evidence Waypoint 分开；
- Leader/TargetFollower 稳定绑定；
- Snapshot 安装、higher-term delivery 都要求真实事件和精确 MessageID；
- stale/same-term、客户端 Request、大 lag 但仍可 Append 等反例有测试；
- 195 条冻结版候选 online/offline mismatch 为 0。

**支持的结论**

- 当前注册的两个 Goal 可以因果地在线判断，也可离线重算；
- 现有实现没有读取未来 Trace；
- 它证明的是“评价机制可用”，不是“Goal 定义已经覆盖所有重要 Raft 行为”。

**证据边界**

Goal 只有两个，且定义者和 operator 设计者相同，存在知识耦合。

**论文用途**

Goal/Waypoint schema、绑定和因果求值属于核心方法，应在主文展开。

**来源**

- [人工 Behavior Goal 与 Waypoint Frontier](manual-behavior-goals-and-waypoints.md)
- Artifact：`/tmp/modelfuzz-ng-waypoint-experiments-20260728-final`

---

### REPLAY-01：Frontier 前缀能否可靠复用

**研究问题**

Frontier 保存的“最好进展”是否能稳定重放，还是一个无法再次到达的偶然状态？

**结果**

- 39/39 exact Trace prefix replay 成功；
- 0 次 Plan prefix 再执行不一致；
- 0 次 online/offline progress 不一致；
- MessageID、Effect、节点状态和 Observation digest 都进入 replay 比较；
- 批量消息 PlanAction 会按真实进展边界裁剪 selector。

**支持的结论**

在当前进程内 etcd-raft backend 和配置中，Frontier seed 可以作为可靠的继续搜索起点。

**尚未排除**

- 外部进程、真实网络、真实磁盘可能引入不可重复性；
- 39 次都来自两个高度受控 Goal；
- top-K 较小且未出现 eviction 压力。

**论文用途**

可在实现正确性中一句话报告成功率，在方法部分说明 prefix 保存和裁剪。

---

### METHOD-01：三方法对照与收益归因

**研究问题**

收益来自普通随机、人工提高相关 Action 概率，还是 Frontier 的进展保留？

**共同配置**

- 3 节点、strict storage-snapshot TLC；
- seeds 101、202、303；
- candidate budget=15、Action budget=1500、max PlanAction=140；
- snapshot threshold=3、retain=1、top-K=6；
- 成功后提前停止；
- 共 18 个搜索、195 个候选。

**结果**

| Goal | 方法 | 成功 | 平均候选 | 平均 Action | 最常停留 |
|---|---|---:|---:|---:|---|
| Snapshot Goal | Unguided | 0/3 | 15 | 190.7 | W2 |
| Snapshot Goal | Goal-aware only | 0/3 | 15 | 689 | W4 |
| Snapshot Goal | Frontier | 3/3 | 10 | 136 | W6 |
| Restart Goal | Unguided | 0/3 | 15 | 190.7 | W2 |
| Restart Goal | Goal-aware only | 3/3 | 5 | 80 | W2 |
| Restart Goal | Frontier | 3/3 | 5 | 40 | W2 |

**当前解释**

- Snapshot Goal：存在 Frontier 独立收益的初步信号，因为相同 hints 的
  Goal-aware-only 没有跨过 W4；
- Restart Goal：成功本身已被 hints 完全解释；Frontier 只减少重复执行的 Action；
- Unguided 对两个 Goal 使用相同生成过程，因此相同 seed 的结果相同，说明 Goal
  evaluator 没有反向影响普通基线。

**不能排除**

- 三个 seed 的调度结果高度相似；
- operator 和 Distance 可能共同编码了 Snapshot 恢复链；
- Goal-aware-only 不保留短前缀，完整 Plan 越来越长，这可能放大 Frontier 优势；
- 成功后提前停止使实际 Action 不相等，只能比较 time/action-to-target 和预算上限。

**论文用途**

这是核心对照，但在扩大 seed、做 top-K/prefix/Distance 消融前只能写作 preliminary。

---

### COVERAGE-01：覆盖广度和目标进展不是同一指标

**关键反例**

- Snapshot Goal / Goal-aware-only：每 seed 有 80 个 v1 状态、40 个 v2 状态，但
  0/3 达到目标；
- Snapshot Goal / Frontier：每 seed 只有 42 个 v1、39 个 v2，却 3/3 达到目标；
- Unguided 每 seed 有 6–7 次“出现新 Facet，但全局 Goal 没推进”。

**支持的结论**

- v1/v2/Facet 适合说明探索广度；
- Goal progress 适合说明是否接近某个具体行为；
- 二者互补，不能互相替代；
- “覆盖数字更大”不能自动解释为更强的特定场景测试能力。

**论文用途**

适合成为评价方法章节的核心示例。

**证据边界**

目前还没有把这些指标与 mutant 检出概率关联，因此不能断言哪种指标更能发现 bug。

---

### SEED-01：当前 seed 规模不足

**观察**

三个 seed 在两种人工 operator 方法中产生了完全相同的成功率、候选数和 Action 数；
许多覆盖计数也相同。

**结论**

当前实验主要验证机制可运行，而不是调度多样性。3/3 不能排除“少量、近似相同轨迹
偶然成功”。

**论文价值**

这是必须公开的限制。未来扩大 seed 后才能升级 METHOD-01 的证据等级。

---

### BUG-01：三类人工 mutant 与检测层归因

**研究问题**

系统是否只会“到达场景”，还是能够检测真实执行链中的错误？

**实验结果**

| Mutant | strict TLC | 无 TLC | 主要检测层 |
|---|---:|---:|---|
| 五节点 `n/3+1` quorum | 100/100 | 109/1000 | strict TLC；无 TLC 等待下游破坏 |
| Snapshot status 映射反转 | 77/100 | 750/1000 | Mapper |
| Restart 丢失 HardState | 100/100 | 995/1000 | Runtime panic / Oracle |

12 组 A/B 共 6,600 个 Plan、1,563,442 个 Action；匹配正常对照均为零失败。

**支持的结论**

- 当前框架能检测三种不同层次的受控缺陷；
- strict TLC、Mapper、Runtime/Oracle 不是重复检测同一类错误；
- 测试系统的最终评价必须包含 bug detection，不能只报告 Goal 或覆盖。

**不能支持**

- 不能证明 Frontier 或 LLM 比随机更会发现这些 mutant；
- 不能把人工 mutant 的检测率解释为真实 bug 发生概率；
- 不能证明能发现尚未知的新 bug。

**论文用途**

是连接方法与实际测试能力的关键基线，未来必须把三种新方法放到相同 mutant 上重跑。

**来源**

- [三类人工缺陷检测实验](experiments/artificial-mutant-detection-20260724.md)
- [n/3+1 quorum 最短反例](experiments/quorum-one-third-mutant-20260721.md)

---

### METHOD-01/ABLATION-01/BUG-GOAL-01：第四轮 10-seed 验证

**实验**

- 14 个稳定性/消融 Campaign，140/140 完成；
- 12 个 control/mutant Campaign，120/120 完成；
- 每组 seeds 4101–4110、3 节点、candidate budget 20、Action budget 3000；
- strict storage-snapshot TLC、worker=1、LLM calls=0；
- weak hint 不读取绑定节点或精确 MessageID，只改变 Action/消息类别权重。

**主要方法**

| Goal | Unguided | strong operators-only | strong Frontier |
|---|---:|---:|---:|
| Snapshot catch-up | 0/10 | 0/10 | 10/10 |
| Restart/higher-term | 0/10 | 10/10，80 Action | 10/10，40 Action |

Goal A 的 Frontier 收益不能由相同 strong operator 单独解释。Goal B 的到达率主要由
strong hint 解释，Frontier 只减少累计 Action。weak Goal B 中 Frontier 为 3/10，
operators-only 为 0/10；该独立信号存在，但 Wilson 区间仍较宽。

**关键消融**

- Goal A K=1/2/4/8 全是 10/10；首次成功 Action 为 105/121/136/136；
- no-prefix 为 0/10，记录 180 次 Waypoint regression、破坏 218 个已完成阶段；
- boolean-only 为 0/10，staged Distance 为 10/10；
- hand-written directed 参考为 10/10，每次 25 Action；
- stable strong Frontier 每个 Campaign 有 10 种精确 Trace，但只有 1 种相对语义
  Trace，扩大 K 没产生有效分支。

**相关 mutant**

| Goal / mutant | Unguided | operators-only | Frontier K=1 | Control false positive |
|---|---:|---:|---:|---:|
| Snapshot status invert | 0/10 | 0/10 | 10/10，105 Action | 0/30 |
| Restart lose HardState | 7/10，mean 173 Action | 10/10，60 Action | 10/10，32 Action | 0/30 |

Snapshot mutant 都是同一 `mapping_failed` 规范化签名；Restart mutant 都是
`oracle_failed / raft.basic:term_regressed`。两类代表性 Frontier 失败分别完成
2 次完整 Trace replay，并由 ddmin 从 15→13、7→4 Action；最终候选各 3/3
保持同签名且 one-minimal。

**覆盖关系**

140 个稳定性运行中有 353 次新 Facet 没有新的全局 Goal progress，也有 6 次新
Waypoint 没有新 Facet。两者互补，不能互相替代。

**支持的结论**

- prefix preservation 与 staged Distance 对当前 Goal A 的收益有因果消融证据；
- 目标路径能显著提高匹配 Snapshot mutant 的检出率；
- Restart mutant 较强，strong hint 已能达到 100%，Frontier 主要减少失败成本；
- control 0 failure、online/offline mismatch 0、replay 全部一致，排除了明显检测器
  假阳性和前缀不确定性解释。

**仍然限制**

- strong operator 的语义轨迹塌缩，10 个 seed 不是 10 个独立高层调度；
- 两个 mutant 都是人工注入，且 Restart 注入较强；
- 当前结果不能外推到其他共识协议或生产 bug；
- 没有 LLM 数据，不能声称 LLM 带来任何收益。

**来源**

- [第四轮 Waypoint Frontier 验证与缺陷检出](waypoint-frontier-validation-and-bug-detection.md)
- Artifact：`/tmp/modelfuzz-ng-round4-direction-a-stability-20260728-final`
- Artifact：`/tmp/modelfuzz-ng-round4-direction-a-mutants-20260728-final`

---

### RANDOM-01：大规模随机基线与零失败解释

**结果**

- 1,473,294 条 300-Action 轨迹；
- 441,988,200 个 Action；
- strict 组三组合计 64,741 条轨迹；
- 正常实现上 Runtime、Mapper、TLC、Oracle、SUT failure 均为 0；
- Snapshot create/send/install/FastForward/reject/status 等能被随机自然触发。

**支持的结论**

- 当前执行框架可进行长时间、大规模随机测试；
- 随机不是完全无效，它能自然触发多种 Snapshot 生命周期；
- 后续方法必须和强随机基线比较时间、Action 和检测率。

**防止错误表述**

- 0 failure 不证明 etcd-raft 或模型无 bug；
- 固定 300-Action horizon 不覆盖更长历史；
- 无 TLC 的 1,408,553 条轨迹不产生 TLA+ 状态覆盖。

**来源**

- [v1 正式随机基线](experiments/v1-random-baseline-20260723.md)

---

### DET-01：确定性、恢复与 artifact 可信度

**证据**

- 21 条完整 Trace 每条重放两次，42/42 成功，累计匹配 3,042 步；
- 三个长跑 seed 重建后的 Action、Effect、模型事件和指标与原 summary 一致；
- checkpoint 中断/恢复和未中断 control 在去除时间字段后报告一致；
- `corpus.jsonl` 可逐字节一致；
- Goal Frontier 另有 39/39 prefix replay。

**支持的结论**

实验结果可复核，失败和成功不是不可重复的偶然输出。

**来源**

- [v1 正式随机基线](experiments/v1-random-baseline-20260723.md)
- [feedback-tuning-v7](experiments/feedback-tuning-v7-20260722.md)
- [人工 Behavior Goal 与 Waypoint Frontier](manual-behavior-goals-and-waypoints.md)

---

### PERF-01：strict TLC 成本与瓶颈归因

**实验**

相同 seed、100-run 配置：

- 严格服务初始约 46.056 秒；
- 优化按需 Action lookup 后约 6.732 秒；
- 两次均为 100/100 成功、2,674 Action、2,197 模型事件、327 个唯一状态。

预热 10 万状态的 invariant cache 基本不改变耗时；profile 指向 434,301 个 Action 的
线性扫描。

**支持的结论**

- 初始性能问题不是 TLA+ 状态校验本身的必然成本；
- 实现层 Action lookup 曾是主要瓶颈；
- 优化未改变实验语义和覆盖结果。

**论文用途**

通常一句话或附录即可，用于解释实验系统为什么能承受多 seed strict TLC。

**来源**

- [strict TLC migration](experiments/strict-tlc-migration-20260721.md)

---

### DIRECTED-01：Snapshot/Partition 参考上界

**结果**

- 5 节点 retain=0：20/20 成功；
- 3 节点 retain=0：15/15 成功；
- 策略稳定完成 partition → majority commit → compaction → heal → MsgSnap →
  install/catch-up；
- 历史 missing-snapshot panic Plan 从 259 Action 缩到 46 Action。

**支持的结论**

Snapshot Goal 不是不可达目标；观察驱动的专用策略可以稳定构造它。

**尚缺**

它没有在当前 Goal A 的相同 candidate/Action/时间预算下作为参考上界运行，因此不能
直接和 METHOD-01 的三个方法比较。

**来源**

- [定向 Snapshot/Partition 与失败 Plan 缩减](experiments/directed-snapshot-minimization-20260722.md)

## 5. 必须保留的失败和无收益结果

| ID | 结果 | 为什么重要 | 禁止的误写 |
|---|---|---|---|
| NEG-01 | Goal A / Goal-aware-only 0/3，停在 W4 | 说明提高相关 Action 概率并不总能完成长因果链 | 不能只展示 Frontier 成功 |
| NEG-02 | Goal B 的成功被 hints 完全解释 | 防止把所有收益归因于 Frontier | 不能写“Frontier 才能到达 Goal B” |
| NEG-03 | semantic gate 只影响 0.2% 运行 | 当前 gate 没有明显效率收益 | 不能把后续收益归因于它 |
| NEG-04 | Random v2 仍是 24/24 新状态 | v2 没有解决全局状态空间问题 | 不能写“v2 已饱和” |
| NEG-05 | Replication Facet 23/24 仍新增 | Facet 也没有天然有限覆盖总数 | 不能写“Facet 数少所以覆盖充分” |
| NEG-06 | 正常长跑 0 failure | 只说明检测器未报告已知类型失败 | 不能写“证明实现正确” |
| NEG-07 | 扩到 10 seed 后 strong 方法仍只有 1 种相对语义 Trace | seed 数不等于调度多样性 | 不能用 10/10 宣称广泛调度泛化 |
| NEG-08 | K=2/4/8 没有比 K=1 增加 reach 或语义分支 | 当前 top-K 容量未被有效利用 | 不能宣称更大的 Frontier 天然更全面 |
| NEG-09 | Goal A operators-only、no-prefix、boolean-only 都为 0/10 | 保存因果前缀和阶段内 Distance 缺一不可 | 不能只展示完整 Frontier |

## 6. 下一阶段会改变研究结论的实验

以下实验按优先级排列。未完成前均不能写成论文结果。

### P0：直接决定当前核心结论

#### NEXT-01：Seed 规模和真实调度多样性

**目的**

排除当前 3 个 seed 只是重复同一条高度确定性轨迹。

**设计**

- 每个 Goal、每种方法至少 10–20 个 seed；
- operator 在合法候选中保留受控随机选择；
- 报告唯一 Plan、唯一 Trace、唯一 waypoint path、绑定节点分布；
- 同时报 candidate、Action、墙钟和 time-to-target 分布；
- 不用单个成功 seed 下结论。

**成功判据**

Frontier 在多数不同 Trace/path 上仍优于 Goal-aware-only，而不是只复现一个模板。

---

#### NEXT-02：Frontier 机制消融

**目的**

区分收益来自 top-K、Distance、prefix preservation 还是 replay 后少执行前缀。

**至少包含**

1. Goal-aware、无 Frontier；
2. Frontier top-K=1；
3. top-K=2/4/8；
4. Frontier 但不锁 prefix；
5. 锁 prefix 但随机选 seed；
6. 保留 Waypoint、移除 Distance；
7. 使用相同 operator 和相同成功停止规则。

**要排除的解释**

- 只是因为完整 Plan 越来越长；
- 只是因为 W6 Distance 特别为 Snapshot 链定制；
- 只是因为保存了一个最短成功模板。

---

#### NEXT-03：新方法上的 mutant/已知 bug 检测

**目的**

把“达到目标”连接到“发现错误”。

**设计**

- 用 Unguided、Goal-aware-only、Frontier 在三类现有 mutant 上做同预算 A/B；
- 正常 control 与 mutant 使用匹配 seed、节点数和配置；
- 指标为首次 failure 的 candidate、Action、墙钟、检测层和失败签名；
- 记录未检测 seed，不只保存成功案例；
- Goal reach 与 bug detection 分开统计。

**关键问题**

Frontier 是否只是更快达到正常行为，还是也更快触发 mutant 的错误后果？

---

#### NEXT-04：外部 Raft issue / 历史 bug benchmark

**目的**

降低“人工 Goal 和人工 mutant 都由作者设计”的同源偏差。

**设计要求**

- 优先选择已证实但目标版本尚未修复、能在本地隔离复现的 issue；
- 固定有 bug 与已修复版本；
- 预先定义成功判据和 Oracle，不根据结果事后改 Goal；
- 保存 issue 链接、commit、复现环境、触发 Plan 和最小化结果；
- 若无法复现，也记录排除原因。

**安全边界**

只测试本地拥有和控制的开源版本，不访问外部服务。

### P1：解释评价标准

#### NEXT-05：行覆盖、v1/v2/Facet、Goal 对 bug 检测的相关性

**目的**

回答哪个指标更适合作为“覆盖广度”，哪个更适合作为“变异/选择反馈”。

**设计**

- 同一批 control/mutant Trace 同时记录行覆盖、v1、v2、五个 Facet、Goal progress；
- 比较首次出现指标 novelty 与首次 failure 的顺序；
- 计算每 1000 Action 的新覆盖和检测概率；
- 避免只比较最终 distinct 数；
- 分析“高覆盖但未发现”“低覆盖但发现”的反例。

**可能结论**

预计不会存在一个指标同时承担所有职责；必须由数据确认，不能预设。

---

#### NEXT-06：Goal/Waypoint/Distance 敏感性

**目的**

检查当前成功是否依赖作者选择的一套“刚好合适”的状态粒度。

**设计**

- 粗 Goal：合并中间 Waypoint；
- 细 Goal：增加 causal evidence；
- 去掉/打乱某个 Waypoint；
- 使用不同 lag bucket、snapshot-required 判据；
- 独立实现者根据文字定义复核；
- 比较误判、不可判定、搜索效率和 bug detection。

---

#### NEXT-07：配置敏感性

至少改变：

- 3/5 节点；
- snapshot threshold、retain entries；
- ElectionTick/HeartbeatTick；
- max Plan length；
- candidate/Action budget；
- crash quota、partition 策略；
- strict TLC on/off。

目的不是把所有配置混成一个平均数，而是说明结论在哪些边界内成立。

### P2：LLM 的独立价值

#### NEXT-08：LLM 增量对照

在人工机制稳定后再比较：

1. Unguided random；
2. 纯代码合法性审查；
3. 人工 Goal-aware operator；
4. 人工 Waypoint Frontier；
5. LLM 只选择注册 operator/branch；
6. LLM 提议受约束的新局部 waypoint 或后缀。

必须保持相同：

- Goal、初始 seed、预算、TLC、Oracle；
- 可选 Action 集；
- LLM 之外的合法性检查；
- 成功停止规则。

必须报告：

- LLM 调用数、失败数、token、单次与累计延迟、费用；
- 被采用/拒绝建议；
- LLM 建议相对纯代码 operator 的独有贡献；
- 达成 Goal 和发现 bug 的独立结果；
- 把 LLM 时间算入墙钟。

只有当 LLM 在 bug detection 或困难 Goal 上提供人工 operator 无法解释的增量，才能写
“LLM 告诉系统下一步怎么测”。

#### NEXT-09：LLM 无收益或错误建议

必须保存：

- LLM 未超过 Frontier 的 Goal；
- 生成不可执行、重复或无进展后缀的比例；
- 长推理导致吞吐下降的情况；
- LLM 选择错误分支而错过 failure 的案例；
- 纯代码已经足够的任务。

这些结果用于界定 LLM 应负责什么，而不是只展示成功 prompt。

## 7. 成本与效率实验的统一口径

任何“更快”“更高效”至少同时报告：

| 维度 | 必报内容 |
|---|---|
| 候选预算 | 上限、实际执行数、首次成功 candidate |
| Action 预算 | PlanAction 与 Concrete Action 分开 |
| 墙钟 | 含 TLC、replay、LLM、持久化 |
| 单次成本 | 每 candidate 和每 Action 的时间 |
| 成功成本 | time/action/candidate-to-target 或 failure |
| 未成功 | timeout、budget exhausted、停滞 Waypoint |
| 并行度 | workers、TLC 串行限制 |
| 提前停止 | 是否成功即停止；不能与用满预算的覆盖数直接比较 |

当前 METHOD-01 使用相同预算上限，但成功组提前停止。因此可以比较首次目标成本，不能把
最终 coverage distinct 直接当作相同工作量下的吞吐结论。

## 8. 配置、版本和可复核性最低要求

每项关键实验必须记录：

- 仓库 commit 或明确的未提交 diff；
- schema 版本；
- etcd-raft/module 版本；
- Goal Definition；
- 完整 CLI settings；
- 节点数、tick、snapshot、fault 配置；
- TLC model/cfg、边界和服务版本；
- seed 列表，而不只写起始 seed；
- candidate、Action、墙钟预算；
- worker 数；
- artifact policy；
- Runtime/TLC/Oracle/LLM 状态；
- online/offline 与 replay 结果；
- 原始 artifact 路径和汇总脚本；
- 正常 control 和失败样本。

如果原始 artifact 已不存在，只能引用当时保存的聚合报告，不得声称完成了新的离线重算。

## 9. 论文内容分层建议

| 内容 | 建议位置 | 原因 |
|---|---|---|
| Goal、Waypoint、因果 evidence | 主文方法 | 核心设计 |
| 三方法对照 | 主文评估 | 区分收益来源 |
| Mutant/历史 bug 检测 | 主文评估 | 连接测试目标与错误发现 |
| LLM 增量与成本 | 主文评估 | 论文核心创新是否成立 |
| Raw/v1 → v2 → Facet | 动机/评价标准 | 解释为什么不用单一完整状态 |
| 字段消融 | 评价标准或附录 | 证明 Facet 不是直觉设计 |
| Online/Offline 一致性 | 实现正确性，一句话+附录 | 排除读取未来信息 |
| Prefix replay | 实现正确性，一句话+附录 | 排除不可重复前缀 |
| Seed/配置敏感性 | 主文简表+附录 | 排除少量偶然成功 |
| 时间、Action、Plan 成本 | 主文表格 | 评价实际效率 |
| 失败/无收益结果 | 主文讨论+附录 | 防止选择性报告 |
| 全量配置、版本、seed、命令 | Artifact/附录 | 保证复现 |

## 10. 当前最谨慎的总体结论

截至 2026-07-28，证据支持：

1. 完整语义状态会因多个有意义但相互组合的字段持续增长，Facet 能更清楚地解释增长来源；
2. 两个人工 Goal 可以因果地在线判断、离线重算并通过确定性前缀继续搜索；
3. Snapshot Goal 上 Frontier 10/10、相同 strong operators-only 0/10，prefix 与
   staged Distance 消融都降为 0/10；
4. Restart Goal 的成功主要由 strong hints 解释，Frontier 把首次成功 Action
   从 80 降到 40；
5. Frontier 对匹配 Snapshot-status mutant 为 10/10，而 unguided 和
   operators-only 都是 0/10；Restart mutant 上 guided 方法均为 10/10，
   Frontier 把首次失败 Action 从 60 降到 32；
6. 当前没有任何关于 LLM 效果的实验结论；
7. 10 个 seed 下 strong 轨迹仍然语义塌缩，top-K 未形成有效分支；下一步仍应修正
   生成多样性和实验配置，不能直接加入 LLM 后宣称提升。

这份结论会随着 NEXT 系列实验更新。任何新结果如果改变上述判断，应先更新本账本，再
修改论文叙事。

## 11. 第五轮：Behavior Branch 与 Diversity-Aware Frontier

以下条目只引用修正统计口径后重新执行的 `final-v7` 实验。更早的
`final`、`v2`–`v6` 目录曾暴露配置默认值、mutant 阈值、Frontier 计数、
Goal 间维度泄漏、pre-decidable bug 归因或 Realized Branch 完整性问题，
因此不得用于论文结论。

### BRANCH-01：Branch schema 与因果分类

- **问题**：同一个 Goal 下能否区分计划路径与真实执行形成的高层因果路径？
- **实现**：新增版本化 schema `raft-behavior-branches-v1-prototype`，显式区分
  `PlannedBranchSignature`、`RealizedBranchSignature`、feasibility、deviation
  和 evidence。
- **判定边界**：只有全部相关维度都已由当前或过去 evidence 判定时，才把路径计为
  完整 Realized Branch；局部 evidence、feasibility 或 deviation 不等价于完整分支。
- **排除解释**：Branch key 不使用原始 NodeID、MessageID、绝对 term/index、
  seed、时间戳或 Trace hash；Goal A/B 只使用各自相关维度。
- **证据**：schema、稳定序列化、ID/term/index 平移、Goal 维度隔离、
  online/offline、prefix replay 和因果 deviation 测试。
- **论文价值**：说明 Branch 表达的是可解释策略路径，而不是给 Trace 换名或通过
  高基数字段制造虚假多样性。

### BRANCH-02：可行性 Pilot 与冻结 Catalog

- **Pilot**：两个 Goal 共 7 个可执行候选分支，每个 3 个 seed，共 21 runs。
- **Goal A**：
  - delayed-delivery：3/3 到达；
  - drop-append：0/3；
  - snapshot-after-heal：3/3；
  - snapshot-failure-retry：0/3；
  - snapshot-before-heal：静态永久不可行，不消耗正式执行预算。
- **Goal B**：
  - higher-term heartbeat：3/3；
  - higher-term MsgApp：3/3；
  - higher-term vote：3/3。
- **证据目录**：
  `/tmp/modelfuzz-ng-round5-branches-pilot-final-v7-20260728`。
- **论文价值**：Catalog 不是只按直觉列举；不可行分支被显式保留为负结果，
  Pilot 成功率不混入正式成功率。

### BRANCH-03：正式多样性实验为负结果

- **矩阵**：冻结 seed `4101..4110`；Goal A 运行 M0–M5，Goal B 运行 M1–M5，
  共 110 runs。
- **主要结果**：
  - M4 Weak Diversity-Aware Frontier：Goal A 0/10，Goal B 0/10；
  - M5 Strong Frontier K=1：两个 Goal 均 10/10；
  - Goal B 的 M1 Weak Operators-Only 仅 1/10，其余 weak 方法未稳定成功；
  - M4 共执行 400 次 Planned Branch 尝试，没有形成完整 Realized Branch；
  - M4 Goal A 的 exact/semantic/progress trace 为 10/9/10，
    Goal B 为 10/8/10，说明执行多样性存在，但没有转化成完整因果分支或 Goal。
- **排除解释**：两种 Frontier 使用相同总容量 4 和相同候选/Action 上限；
  所有正式 run 的 LLM calls 为 0、online/offline mismatch 为 0、prefix replay 成功。
- **证据目录**：
  `/tmp/modelfuzz-ng-round5-branch-diversity-stability-final-v7-20260728`。
- **论文价值**：能够区分“Trace 看起来不同”和“形成可判定、高层因果路径”；
  当前 weak 瓶颈不能归因于 seed 完全相同，也不能宣称 Branch Frontier 已带来收益。

### BRANCH-04：容量和维度消融不支持单调收益

- **矩阵**：Goal B，5 个代表性设置 × 10 seeds，共 50 runs。
- **结果**：
  - realized-aware C=1：0/10；
  - C=2：1/10；
  - C=4：0/10；
  - C=8：0/10；
  - planned-only C=4：0/10；
  - 去掉 key-message 维度 C=4：0/10。
- **解释边界**：C=2 的单个成功不能解释为容量提升，也不能证明 Realized-aware
  Frontier 的稳定收益；容量没有单调趋势。
- **证据目录**：
  `/tmp/modelfuzz-ng-round5-branch-diversity-ablations-final-v7-20260728`。
- **论文价值**：排除“只要增加 K/容量就会产生独立有效分支”的简单解释。

### BUG-03：Branch 多样性没有稳定保持两类 mutant 能力

- **矩阵**：Restart/Snapshot 两类 mutant 及对应 control，4 个代表方法，
  每组 5 seeds，共 80 runs。
- **Restart lose-HardState**：
  - weak diversity C=4：3/5；
  - weak standard C=1：1/5；
  - weak standard C=4：0/5；
  - strong K=1：5/5，首次失败 Action 32。
- **Snapshot status-invert**：
  - 三个 weak 方法均 0/5；
  - strong K=1：5/5，首次失败 Action 105。
- **Control**：全部 false positive 为 0。
- **per-Planned Branch**：Restart diversity 的 3 个失败分别归因于 vote 1 次、
  heartbeat 2 次；失败发生在完整 Realized Branch 可判定之前，因此不得伪造
  per-Realized Branch 检出结论。
- **证据目录**：
  `/tmp/modelfuzz-ng-round5-branch-diversity-mutants-final-v7-20260728`。
- **论文价值**：Restart 的 3/5 是积极信号，但 Snapshot 能力仍完全依赖 strong
  人工知识上界；Branch 多样性尚未稳定保持第四轮能力。

### REPLAY-03：第五轮代表失败可重复且可最小化

- Snapshot failure replay：17/17 matched；
- Restart failure replay：7/7 matched；
- Snapshot ddmin：15 Actions 缩减为 13，最终 3/3 稳定，
  normalized signature 为 snapshot mismatch；
- Restart ddmin：7 Actions 缩减为 4，最终 3/3 稳定，
  normalized signature 为 term regression；
- 两个结果均通过 one-minimal 检查。
- **证据位置**：第五轮 mutant 根目录下的 `failure-analysis/`。
- **论文价值**：失败不是只在在线搜索中偶然出现；最小化结果与稳定 failure
  signature 连接了“到达路径”和“实际发现错误”。

### BRANCH-05：Facet、Goal 与 Branch 是互补信号

- M4 Goal A：new Facet without Goal progress 45，new Waypoint without new
  Facet 1，new Branch without new Facet 0，new Facet without new Branch 69；
- M4 Goal B：对应为 68、6、0、83；
- **解释**：Facet 能发现大量局部语义变化，但这些变化没有自动组成完整 Branch；
  Waypoint/Goal 表达目标推进，Branch 试图表达到达同一 Goal 的路径类别。
- **论文价值**：不能用 Facet novelty、Goal progress 或 Branch count 中任意一个
  单独替代另外两个。

### BRANCH-06：第五轮决策

证据选择方向 A：先修正 Branch evidence 完整性、weak mutation、
Distance/progress 与去重关系，再考虑 Stall Detector。当前不满足方向 B 的前提，
也不进入 LLM Planner：

- 两个 M4 均为 0/10；
- 400 次 Planned Branch 尝试没有完整 Realized Branch；
- C=2 只有单 seed 成功，容量无单调收益；
- strong 路径仍塌缩；
- Restart mutant 有局部积极结果，但 Snapshot mutant 未由 weak 方法检出。

完整设计、实验表、负结果和复现命令见
`docs/behavior-branch-diversity-and-frontier.md`。

## 第六轮：Partial Branch Evidence 与阶段预算

### EVIDENCE-01：C=2 成功来自连续前缀链，不是两种 Branch

- 接受的原始目录：
  `/tmp/modelfuzz-ng-round6-c2-differential-raw-v2-20260728`；
- 当前离线差分：
  `/tmp/modelfuzz-ng-round6-c2-analysis-v4-20260728`；
- C=2 的 Goal B seed 4101 在 candidate 17 成功；C=1/4/8 均失败；
- Frontier parent 在 candidate 7 先分叉，实际 Action 对 C=1 在 candidate 8、
  对 C=4/8 在 candidate 9 分叉；
- candidate 15 和 16 都是 `goal-b-higher-term-vote` 实例，前者是后者的直接
  parent，不是两个不同语义 Branch；
- 逐 Action 记录 1409 条，prefix replay 96/96，online/offline mismatch 0，
  LLM calls 0。
- **证据等级**：个案诊断，不支持 C=2 的统计优势。

### EVIDENCE-02：单 Branch 独占预算仍不能解决形成障碍

- 7 条 Branch × 10 seed；每条独占 C=1、20 candidate；
- Goal A 四条可行 Branch 均为 0/10，最深 W2；
- Goal B 三个 planned label 各有 1/10 Goal，但 Heartbeat/MsgApp 的成功实际
  都偏移为 Vote；只有 Vote 形成过 planned Commitment；
- 搜索行为的原始能力结论来自
  `/tmp/modelfuzz-ng-round6-single-branch-v2-20260728`；
- 由于 Stable Key 浅拷贝影响具体 Evidence step 产物，最终 artifact 在
  `/tmp/modelfuzz-ng-round6-single-branch-v3-20260728` 重跑，不选择性复用
  旧 step 记录。
- **证据等级**：能力诊断，不用于公平性能排名。

### EVIDENCE-03：修复后 Pilot 只支持继续检验，不证明 Goal 收益

- 接受目录：`/tmp/modelfuzz-ng-round6-evidence-pilot-v4-20260728`；
- Goal A E0/E1/E2 的 Goal 和 Commitment 都是 0/5；
- Goal B E0/E1/E2 Goal 都是 0/5，Commitment 分别为 0/5、1/5、2/5；
- Goal B 总 Action 分别为 549、1118、611；
- online/offline mismatch 0，LLM calls 0。
- **证据等级**：Pilot；只用于冻结 30-candidate 正式矩阵。

### EVIDENCE-04：最终公平正式实验不支持 Stage，也未证明 Evidence 优于 Standard

- 接受目录：`/tmp/modelfuzz-ng-round6-formal-v4-20260728`；
- 2 Goal × 6 方法 × 10 seed = 120 个运行；每组相同 30 candidate /
  4500 action 上限；
- Goal A M1–M5 都是 0/10，Strong 为 10/10；
- Goal B：M1 0/10、M2 Standard 7/10、M3 Diversity 1/10、
  M4 Evidence RR 7/10、M5 Evidence Stage 3/10、Strong 10/10；
- Goal B M2 使用 221 candidate / 1398 action，M4 为 221/1411；
  Evidence RR 没有显示成功率或成本优势；
- Goal B M4/M5 都有 5/10 seed 到达 Commitment；M5 的 committed
  candidate 更多（12 对 5），但 Goal 更少（3/10 对 7/10）；
- Goal A M5 只少用 9 candidate 和 44 action，仍无 Commitment/Goal；
- 全矩阵 prefix replay 2160/2160，online/offline mismatch 0，LLM calls 0，
  正式 control bug detection 0。
- **结论**：Supported/Commitment 是有解释力的诊断层，但当前 Stage 预算没有
  提高测试能力；Evidence 搜索也没有优于普通 Standard C=1。

### EVIDENCE-05：两次正式目录因审计问题整体排除

- `/tmp/modelfuzz-ng-round6-formal-20260728`：priority multiplier 未显式进入
  settings，运行中停止；
- `/tmp/modelfuzz-ng-round6-formal-v2-20260728`：Stable Key 归一化对 slice
  浅拷贝，反向清空具体 Evidence step/support；
- `/tmp/modelfuzz-ng-round6-formal-v3-20260728`：manifest 布尔默认使用 OR，
  M1/M2/M6 无法显式关闭 Branch 规划，Goal B Strong 从已复现 10/10 降为
  0/10；
- 修复分别有回归测试，并且最终 `formal-v4` 的归一化 manifest 已核对
  M1/M2/M6=false、M3/M4/M5=true。
- **论文价值**：排除实现/配置解释，避免把错误配置造成的差异归因给方法。

### EVIDENCE-06：Mutant 检出仍以实际 Bug 为最终标准

- 接受目录：`/tmp/modelfuzz-ng-round6-mutants-v1-20260728`；
- Snapshot：Standard 0/5、Evidence Stage 0/5、Strong 5/5；
- Restart：Standard 4/5、Evidence Stage 5/5、Strong 5/5；
- 六个 control campaign 共 0/30 false positive；
- Restart Evidence 的 5/5 是比 Standard 4/5 多一个配对 seed 的积极信号，
  但五次 Bug 都发生在 Commitment 前，不能归因给完整 planned Branch；
- Snapshot Trace replay 17/17，ddmin 15→13，3/3 稳定，
  one-minimal=true；
- Restart Evidence Trace replay 8/8，ddmin 8→4，3/3 稳定，
  signature 为 `raft.basic:term_regressed`，one-minimal=true；
- mutant 全矩阵 prefix replay 1347/1347，online/offline mismatch 0，
  LLM calls 0。
- **结论**：Evidence 对 Restart 有小样本积极信号，但没有解决 Snapshot，
  也没有在公平 Goal 实验中优于 Standard；下一轮仍选择方向 A。

完整设计、口径和正式表见
`docs/partial-branch-evidence-and-stage-budgeting.md`。

## 第七轮：Focused Protocol-Aware Local Mutation 与方法冻结

### FOCUSED-01：单步建议失败揭示 Standard Frontier 的局部协调缺口

- 初版 Advisor 每次只追加一个 MsgApp 或 MsgAppResp；
- Goal A 反复从同一 W3 前缀选择相同消息，因为单次交付没有改变 Waypoint 或
  staged distance，Standard Frontier 不保留该中间前缀；
- 改为最多 9 个相邻动作的有限多数派窗口后，5-seed strict Pilot 从 legacy
  0/5 提升为 focused 5/5；
- 该窗口不选择最终 MsgSnap，不包含完整 Goal Plan。
- **论文价值**：解释为什么“权重知道该投递 MsgApp”仍不足，也说明局部协议协调
  是独立于 Frontier 抽象的生成问题。

### FOCUSED-02：正式 M0–M4 支持 focused 主线

- 接受目录：`/tmp/modelfuzz-ng-round7-focused-formal-v1-20260729`；
- 2 Goal × 5 方法 × 10 seed = 100 个 strict TLC 运行；
- Goal A：M0 0/10、M1 0/10、M2 9/10、M3 9/10、Strong 10/10；
- Goal B：M0 1/10、M1 5/10、M2 10/10、M3 10/10、Strong 10/10；
- M2/M3 逐 seed Plan/Trace/语义 Trace/Goal path/Facet path mismatch=0；
- 全部 Online/Offline mismatch=0，前缀 replay 成功且执行不一致为0，LLM=0；
- **论文价值**：Focused 直接修复第六轮定位的候选生成瓶颈；收益不来自
  Branch/Evidence 在线引导。

### FOCUSED-03：消融既有正结果，也有必要保留的负结果

- 接受目录：`/tmp/modelfuzz-ng-round7-focused-ablations-v1-20260729`；
- Goal A Full 4/5、No-Boundary 2/5、No-Target-Suppression 4/5；
- 初次 No-Quorum 实现仍从 request 宏携带复制窗口，结果无效；修复并测试后，
  `/tmp/modelfuzz-ng-round7-no-quorum-corrected-v1-20260729` 为 0/5，均只到W2；
- Goal B Full 5/5、No-Log-Freshness 5/5、No-Vote-Completion 4/5、
  Early-Restart 4/5；
- **论文价值**：多数派维护与 compaction boundary 是 Goal A 的实质因素；
  当前三节点环境不能证明 Target suppression 和 log freshness 的独立收益。

### FOCUSED-04：Focused 提高已知人工缺陷检出

- 接受目录：`/tmp/modelfuzz-ng-round7-focused-mutants-v1-20260729`；
- Snapshot status invert：legacy 0/5、focused 4/5、Strong 5/5；
- Restart lose HardState：三种方法均 5/5；focused 固定第4候选/36累计Action，
  legacy 首次失败候选为6–23；
- 30 条对应 control 为 0 false positive；
- Snapshot 代表 Trace replay 21/21 ×3，ddmin 17→13，稳定3/3；
- Restart 代表 Trace replay 12/12 ×3，ddmin 11→4，稳定3/3；
- 两者均 one-minimal，分别是 mapping 与 concrete Oracle 检测；
- **论文价值**：把“到达人工 Goal”连接到“发现已知缺陷”，是本轮最重要的外部
  有效性证据；人工 mutant 仍不能称为生产 bug。

### FOCUSED-05：路径集中是明确限制

- 正式每组 10 seed 都有 10 个不同精确 Trace；
- Goal A 相对语义 Trace：legacy M1=7、focused M2=2、Strong=1；
- Goal B：M1=5、M2=1、Strong=1；
- Goal progress path：A focused=10，B focused=6；
- **结论**：focused 没有固定精确 Trace，但语义调度明显集中；成功率提升不能
  表述为全面路径多样性提升。

### FOCUSED-06：方法冻结

- 推荐主线：
  `Facet + Waypoint Frontier + Protocol-Aware Local Mutation`；
- Branch/Evidence 冻结为诊断、artifact、未来 LLM 输入候选和非默认实验功能；
- Strong 保留为能力上界；
- 暂缓 LLM、Goal-local Stall、新 Frontier 和新抽象；
- 下一步优先冻结论文实验并验证 Raw/v2/Facet 同预算广度，再检查五节点和更多 Goal。

完整实现、34项报告和不能推出的结论见
`docs/focused-protocol-aware-mutation-and-method-freeze.md`；冻结边界见
`docs/branch-evidence-freeze.md`。

## 第八轮：Facet-Guided Corpus 与行为广度

### FACET-ONLINE-01：在线与离线使用同一 Observation

- 正式 G0～G4 共 50 个 campaign、3,000 条候选，Raw、v2、五个 Facet、四类
  Interaction、StableKey 和准入决策重算 mismatch 全部为 0；
- Legacy 10 个 campaign、600 条候选，同样为 0 mismatch；
- Mutant 因 `artifact_policy=failures` 只保留失败 run：Snapshot 比较 163 条、
  Restart 81 条、Quorum 1,500 条，可比较项 mismatch 为 0；
- control 成功 run 没有完整目录，不能把 decision mismatch=0 扩大表述为完整
  Observation 已逐字段比较。
- **证据目录**：
  `runs/facet-guidance-formal-20260729`、
  `runs/facet-guidance-legacy-formal-20260729` 和三个 mutant 根目录。
- **论文价值**：排除在线分类器与离线评价器不一致造成的虚假收益。

### FACET-ONLINE-02：Facet-Fixed 提高跨指标行为广度并压缩重复 Corpus

- 10 个共同 seed、每 campaign 60 candidate、fixed energy=2、FIFO-once、
  Corpus cap=128；
- G3 Facet-Fixed 对 Random：Raw 1,103.9 对 930.2（+18.7%），v2 637.4
  对 495.6（+28.6%）；
- Election/Replication/Snapshot/Recovery/Network 分别为
  38.7/139.6/36.9/18.9/17.3，Random 为 33.5/120.8/34.1/15.0/14.8；
- 四类 Interaction 计数之和为 127.0，Random 为 110.6；Interaction 不参与
  G3 准入，因此它是外部评价信号；
- Corpus 均值 28.7 对 60.0，语义重复率 0.33% 对 24.33%，每次准入新增跨指标
  单元 67.06 对 29.24；
- Action 吞吐 181.84/s 对 147.89/s；完整记录配置下没有观察到吞吐损失；
- Cliff's delta：Raw 0.54、v2 0.80、Replication 0.50、Recovery 0.41、
  Network 0.42；n=10，不宣称统计显著。
- **证据等级**：A（当前配置和预算范围内）。
- **论文价值**：支持 Facet 从离线解释指标升级为显式在线全局 Corpus 指导。

### FACET-ONLINE-03：Interaction 在线准入没有增加稳定收益

- G4 的 Election 40.2 和四类 Interaction 计数之和 127.5 略高于 G3 的
  38.7/127.0；
- 但 G4 的 Raw、v2、Replication、Snapshot、Recovery、Network、AUC 和 parent
  yield 多数低于 G3；
- Corpus 语义重复率两者相同，均为 0.33%；
- **状态**：正式负结果；
- **论文价值**：排除“把更多语义组合放进准入一定更好”，支持第一版只使用独立
  Facet，Interaction 保持离线评价。

### FACET-ONLINE-04：广度提升没有自动变成深层 Goal 提升

- Goal A campaign：G3 3/10，Random 5/10；两者在 600 条候选中的目标 run 都是
  24；
- Goal B：G0～G4 全部 0/10；G3 唯一产生 18 条 W3 轨迹，但没有到达最终目标；
- G3 的 Semantic Trace 均值 45.4，与 Random 相同；
- **状态**：正式混合/负结果；
- **论文价值**：证明 Facet 负责全局广度，不能替代 Waypoint Frontier 和
  Protocol-Aware Local Mutation 的深度职责。

### FACET-ONLINE-05：当前三个 mutant 不能区分指导方法

- Snapshot、Restart、Quorum 三个 mutant 中，G0～G4 每种模式均为 5/5 检出；
- Snapshot 首次失败均值 candidate 1.8 / 139.4 Action，Restart 为
  2.0 / 167.8 Action，各模式相同；
- Quorum 所有 campaign 在 candidate 1 / 100 Action 失败，每种模式的 300 条
  候选中有 295 条 `disabled_action BecomeLeader` 和 5 条
  `multiple_leaders_same_term`；
- 三节点和五节点 control 两个矩阵合计 0/50 false positive；
- 每模式 5/5 的 Wilson 95% 区间为 [0.566,1.000]，0/5 的上界仍为 0.434；
- **状态**：检测能力正结果、方法区分负结果；
- **论文价值**：系统能检测缺陷，但 mutant 在 guidance 产生分叉前就暴露，不能
  支持 Facet 提高缺陷检出率的主张。

### FACET-ONLINE-06：两个代表失败可确定重放和最小化

- Snapshot Trace replay 74/74 matched，ddmin 74→14，117 次尝试，最终
  3/3 稳定，one-minimal=true；
- Restart Trace replay 89/89 matched，ddmin 90→25，331 次尝试，最终
  3/3 稳定，one-minimal=true；
- Quorum replay/ddmin 按当前决定跳过，不写成已完成；
- **证据位置**：两个代表 run 目录下的 `replay/` 和 `minimized/`；
- **论文价值**：把早期 mutant 检出连接到可审计的确定执行和稳定 failure
  signature，同时保留未运行项边界。

### FACET-ONLINE-07：第八轮决策

- 选择方向 A：保留 `facet-fixed` 作为显式在线全局广度模式；
- 不把 Facet 立即改成普通 fuzz 默认值；
- 不采用 G4 作为第一版在线准入；
- 五个 Facet、四类 Interaction、Raw/v2 和 Goal 继续完整离线记录；
- 下一阶段应先使用更深、具有区分力的历史 bug 或 mutant，再评价缺陷检出；
- 后续 LLM 应承担多步测试规划，不能代替 coverage 计数和合法动作检查；
- 本轮 LLM calls=0，不支持任何 LLM 有效性结论。

完整设计、正式矩阵、负结果和复现入口见
`docs/facet-guided-corpus-and-breadth-evaluation.md`。

## 第九轮：Facet、Waypoint 与 focused mutation 的分层组合

### BREADTH-DEPTH-01：两阶段隔离与版本化 Handoff 已实现

- schema：`raft-breadth-depth-handoff-v1-prototype`；
- Global Corpus 冻结后，按 Waypoint、staged Distance、身份无关多样性、prefix
  长度和 StableKey 选择 Handoff；
- Local 阶段保持 Standard Frontier、prefix preservation 和 frozen focused
  Advisor，不回写 Global Corpus；
- Global/Local candidate 与 Action 预算之和强制等于总预算；
- **证据等级**：实现、单元测试和 strict TLC 端到端。

### BREADTH-DEPTH-02：Pilot 冻结 60/30 与 K=1

- seed 9301–9305；
- 30/60 为 Goal A 5/5、Goal B 3/5；
- 45/45 与 60/30 K=4 都为 A 5/5、B 4/5；
- 60/30 K=1 reach 和成本与 K=4 相同；K=4 选择 40 个 seed，但容量 1 的局部
  Frontier 每 campaign 最终只保留一个；
- **决策**：正式矩阵使用总 90 candidate、60/30、K=1。

### BREADTH-DEPTH-03：Facet 是两阶段方法中最强的 Global 广度

- 正式 M0～M5、2 Goal、seed 9501–9510，共 120/120 campaign 完成；
- 相同 60 Global candidate 下，M5 的 Raw/v2/Facet/Interaction 均值为
  1614.5/949.5/349.8/165.2；
- M2 为 1324.8/734.9/301.8/154.1，M3 为
  1395.4/811.2/319.2/153.7，M4 为 1453.2/855.5/326.1/156.7；
- M5 对 M1 的 Final Raw/v2/五 Facet/四 Interaction/Semantic Trace
  Cliff's delta 均为 1.0；
- **论文价值**：再次确认 Facet 的职责是全局广度，并证明两阶段 Final union
  没有丢弃全局 coverage。

### BREADTH-DEPTH-04：当前 Handoff 不把广度稳定转化为深度

- Goal A：M0 3/10、M1 10/10、M2 3/10、M3 5/10、M4 3/10、M5 3/10；
- Goal B：M0 1/10、M1 10/10、M2 9/10、M3 8/10、M4 9/10、M5 8/10；
- M5 的完整 Action 均值为 A 11,851.4、B 10,827.0，M1 仅
  399.9/151.7；
- Random 的 Goal A 初始 Waypoint 更深，v2 的 Goal B W3 entry 更多；
- 一个 M5/Goal A seed 的 30 次局部 mutation 全部被合法性检查拒绝；
- **状态**：正式负结果；广度收益不等于 Goal 深度收益。

### BREADTH-DEPTH-05：Handoff 缓解成功相对语义 Trace 集中

- M1 两个 Goal 各有 10 个不同 exact Trace，但相对 Semantic Trace 都只有 1；
- M5 Goal A 的 3 个成功对应 3 个相对 Trace，Goal B 的 8 个成功对应 8 个；
- M2～M5 的成功一般也来自不同 Handoff semantic class；
- 但 M5 的成功数低于 M1，且 Goal A progress path 只有 2；
- **结论**：路径集中得到局部缓解，不能抵消 reach 损失。

### BREADTH-DEPTH-06：Replay、Online/Offline 与预算口径稳定

- Global 3300/3300 candidate 成功；
- Handoff replay 3894/3894，prefix replay 1804/1804；
- 1794 个已生成局部 candidate 全部执行 strict TLC，可执行失败为 0；
- Online/Offline、prefix execution、Observation、MessageID mismatch 均为 0；
- 120/120 `global_coverage_retained=true`，LLM calls=0；
- 未到达和零执行候选的运行都标记为 censored/budget-exhausted。

### BREADTH-DEPTH-07：回归与 threshold=5 泛化

- Snapshot/Restart control 各 0/5 false positive、各 5/5 Goal reach；
- Snapshot status invert 与 Restart lose HardState 各 5/5 检出；
- Snapshot replay 21/21、ddmin 17→13；Restart replay 12/12、ddmin 11→4；
  两者最终签名均 3/3 稳定且 one-minimal；
- threshold=5：M1 Goal A/B 为 0/5、5/5，M5 为 1/5、3/5；
- 泛化 Handoff replay 278/278、prefix replay 691/691、mismatch=0；
- **边界**：n=5 只证明机制可工作和结果分化。

### BREADTH-DEPTH-08：第九轮决策

- 选择方向 B；
- `facet-fixed` 保留为显式全局广度模式；
- `waypoint-frontier + raft-focused` 保留为显式 Goal 深度模式；
- `breadth-depth-benchmark` 保留为研究组合和 Handoff 诊断，不设为默认；
- 若继续，只做有限 Handoff 修正和小规模未选择 seed 反事实评价；
- 不修改 Facet、Goal、Waypoint 或 focused Advisor，不进入 LLM 阶段。

完整实现、正式统计、泛化、限制与复现见
`docs/facet-waypoint-breadth-depth-combination.md`。
