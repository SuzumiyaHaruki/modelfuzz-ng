# Facet-Guided Corpus 与分布式行为广度评估

日期：2026-07-29
报告状态：正式广度矩阵、Legacy 参考矩阵和三类 mutant/control 已完成；Snapshot 与
Restart 代表失败已重放和最小化；按当前决定跳过 Quorum 代表失败的重放与最小化。

## 结论摘要

本轮在相同 Runtime、Raft Adapter、变异器、candidate 预算、Plan 长度上限、fixed
energy 和父 Plan 调度下，只改变 Corpus 准入信号。10 个共同 seed 的正式结果支持把
五个独立 Facet 作为第一版在线全局 Corpus 指导信号：

- `facet-fixed` 相对 Random 的最终 Raw distinct 提高 18.7%，v2 distinct 提高
  28.6%，五个独立 Facet 均提高；
- `facet-fixed` 的 Corpus 语义重复率均值为 0.33%，Random 为 24.33%；
- `facet-fixed` 只保留平均 28.7 个 Corpus entry，Random 保留 60 个，但每个准入
  Plan 平均贡献 67.06 个跨指标新单元，Random 为 29.24；
- `facet-fixed` 的 Action 吞吐为 181.84/s，Random 为 147.89/s，没有观察到因
  Facet 指导而造成的吞吐下降；
- `facet-interaction-fixed` 没有稳定超过 `facet-fixed`，第一版没有必要把
  Interaction 放入在线准入；
- Goal A 的 campaign 到达率是 `facet-fixed` 3/10、Random 5/10，Goal B 所有固定
  模式都是 0/10；因此广度提升没有自动转化为深层 Goal 提升；
- Snapshot、Restart、Quorum 三类 mutant 中，所有模式都是 5/5 检出，无法区分
  指导方法。Quorum 更是在第一个 candidate 就失败，明显过于容易；
- control 为 0 false positive，但每个模式只有 5 个 seed，置信区间仍宽。

因此，本轮选择“方向 A，但保持窄结论”：Facet 适合在线管理全局行为广度；Waypoint
和 Goal 仍负责深层目标，Interaction 暂时只做离线评价。当前结果不能证明 Facet
提高了缺陷检出率，也没有运行 LLM。

## 1. 本轮动机

Raw TLA+ State 和完整 v2 Semantic State 都把多个协议维度组合成一个完整状态。
Election、Replication、Snapshot、Recovery 和 Network 中任一局部变化，都可能制造
一个新的完整组合。这样得到的状态数很大，却不容易回答“测试到底扩展了哪个协议
行为”。

此前离线分解已经证明，不同 Facet 的增长和饱和速度不同。本轮进一步回答：把这些
独立 Facet 真正接入普通 fuzz 的在线 Corpus 后，能否在固定预算内减少重复并扩大
行为广度，而不只是让离线报表更容易解释。

## 2. Facet 已有证据

此前 32 份 artifact、4,255 个 CoverageFrame 的字段消融显示，完整 v2 的主要增长
来自节点形状、lag、日志和 Snapshot 形状的组合，而不是单一绝对 term 或 index。
删除 `canonical_node_shapes` 后 distinct 从 1,110 降到 366，删除 lag 组后降到
484。这个结果支持把组合状态拆成独立行为维度。

此前还观察到不同 Facet 的饱和位置不同，且“新 Facet”与“新 Waypoint”并不互相
包含。这些证据只说明 Facet 的设计有依据，不能代替本轮在线指导实验。

## 3. 为什么需要在线引导实验

离线 Facet 数量高，不等于 Facet 适合指导搜索。在线使用后还可能出现：

- Corpus 太早饱和，后续没有可变异父 Plan；
- 只优化自己的 Facet 指标，却损害 Raw、v2 或其他行为；
- 准入计算和 artifact 写入降低吞吐；
- Corpus 变小，但保留下来的 Plan 并没有更高子代产出；
- 覆盖更广，却没有更容易到达 Goal 或发现缺陷。

因此本轮把引导信号与评价指标分开：无论使用哪种准入方式，所有 campaign 都记录
Raw、v2、五个 Facet、四类 Interaction、Goal、failure 和成本。

## 4. Guidance 接口

`internal/coverageguidance` 提供协议无关接口：

```go
type CoverageGuidance interface {
    Observe(CoverageObservation) (Decision, error)
    Snapshot() Snapshot
}
```

接口只比较已经生成的稳定 coverage unit，不识别 Raft term、Leader、MsgApp、
MsgSnap、commit index 或节点身份。Raft 专用的状态解释位于
`internal/coverageanalysis` 和 `internal/model/raft`。

冻结 schema 是 `raft-online-coverage-guidance-v1-prototype`。每次决策保存观察量、
新增量、是否准入、原因、Corpus 前后大小、父 Plan、fixed energy 和
`StableDecisionKey`。

## 5. CoverageObservation

每个候选执行形成一条确定性 Observation，主要字段包括：

- RunID、CandidateID、ParentPlanKey、PlanKey、TraceKey；
- ActionCount、ModelEventCount、ElapsedMillis；
- Raw TLC fingerprint 及可读原值；
- v2 key 及可读序列化值；
- 五个独立 Facet 的 key/value；
- 四类 Interaction 的 key/value；
- Runtime、TLC、Oracle 和 failure signature；
- SemanticTraceDigest、可选 OfflineGoals；
- Raw、v2、CoverageFrame、Facet 和决策耗时；
- StableKey。

每个 coverage unit 同时保留稳定 key 和可读值，没有只存 hash。五个 Facet 始终是
五个独立集合，没有重新拼成完整状态。

## 6. 在线与离线 Facet 对齐

在线和离线都调用
`coverageanalysis.BuildCoverageObservation`。它复用冻结的 CoverageFrame 和 Raft
Facet projection：

- 一个 Action 对应多个 model event 时逐事件推进；
- Partition/Heal 即使没有 model event，也通过 stutter frame 更新 Network Context；
- Crash/Restart 更新 Lifecycle Context；
- 只读取当前及历史执行，不读取未来 Trace；
- 节点 ID、绝对 term/index 和 MessageID 不进入 Facet key。

正式 G0～G4 的 3,000/3,000 条 Observation 全部可比较，Raw、v2、Facet、
Interaction、StableKey 和决策重算 mismatch 均为 0。Legacy 的 600/600 条也为 0。

Mutant 使用 `artifact_policy=failures`，因此成功运行没有完整 run 目录：Snapshot
只能完整比较 163 条失败 artifact，Restart 为 81 条，Quorum 因每条都失败而比较
1,500/1,500；这些可用 artifact 的 mismatch 都为 0。不能把未保存的 control 成功
run 说成已经完成完整 Observation 比较。

## 7. G0～G4 定义

| 模式 | Corpus 准入条件 | 是否读取 coverage 决策 |
|---|---|---|
| G0 `random` | 成功且 PlanKey 唯一即准入，直到 Corpus 上限 | 否 |
| G1 `raw-fixed` | 至少一个新 Raw TLC fingerprint | 只读 Raw |
| G2 `v2-fixed` | 至少一个新 v2 State | 只读 v2 |
| G3 `facet-fixed` | 五个独立 Facet 中任一个出现新值 | 只读 Facet |
| G4 `facet-interaction-fixed` | 新 Facet 或四类冻结 Interaction 中任一新值 | 读 Facet 和 Interaction |

G0 不是“Corpus 为空时的随机回退”，而是显式的无 coverage 准入基线。所有模式仍
record-only 地计算全部评价指标。

## 8. Legacy-Raw 定义

`legacy-raw` 保留历史 Raw 门槛、动态 energy 和既有队列行为。它使用相同的 10 个
seed、60 candidate 和 Plan 上限，但不是 fixed-energy 公平矩阵的一部分。

Legacy 的均值为 Raw 1,359.3、v2 784.9、Election 47.7、Replication 164.0、
Snapshot 41.2、Recovery 20.5、Network 19.0，Action 吞吐 201.56/s。它在多个数字
上高于 G0～G4，但差异同时包含历史准入、energy 和调度语义，不能归因于“Raw
指导优于 Facet”。本轮只把它作为兼容和历史参考。

## 9. Fixed energy

Pilot 后冻结 `fixed_energy=2`。G0～G4 的每个被选择父 Plan 都获得相同的两个子候选
机会，不按新状态数、Facet 数、稀有度、Goal、Bug 或 Trace 长度动态加权。

这隔离了“准入什么”与“一个准入项获得多少后续预算”。Legacy 不使用这一公平
语义。

## 10. Parent selection

G0～G4 使用 `admission-fifo-once`：父 Plan 按准入顺序进入固定队列，每个父 Plan
只被调度一次，并获得固定 energy。该规则简单、确定，不根据 coverage 数量重新
排序。

正式实验中，G3 平均选择 27.0 个父 Plan，22.2 个父 Plan 产生过新 coverage 子代；
G0 的 Corpus 虽然更大，但这不会让某个高覆盖父 Plan获得额外 energy。

## 11. Corpus admission

Controller 先计算本条 Observation 相对全局集合的新颖性，再按模式决定是否准入。
失败执行、空 PlanKey、重复 Plan 和达到 Corpus 上限会被统一拒绝。每次决策都写入：

- `CoverageUnitsObserved`；
- 分维度 `NewCoverageUnits`；
- `WasAdmitted` 和 `AdmissionReason`；
- `CorpusSizeBefore/After`；
- `ParentPlanKey`、`FixedEnergy`；
- `StableDecisionKey`。

Random 的准入决定在查询 coverage novelty 之前已经确定；后续合并 coverage 只用于
record-only 评价。

## 12. 实验公平性

G0～G4 共用：

- 同一 etcd-raft Runtime、Adapter、Action、Resolver、Mapper 和 TLA+ 模型；
- 同一随机变异器及合法动作检查；
- 同一 Crash/Restart、Partition/Heal 和 Snapshot 能力；
- 同一 strict TLC 和 Oracle；
- 10 个共同 seed；
- 每 campaign 60 个 candidate、单 Plan 最多 80 个 action；
- initial population 4、parallelism 1；
- fixed energy 2、FIFO-once、Corpus 上限 128、ready queue 上限 256；
- snapshot threshold 2、retain entries 1；
- Goal、Waypoint、focused mutation、Branch/Evidence 和 LLM 均不参与搜索。

所有模式均为 60/60 candidate 成功执行，candidate legal rate 和 execution rate 都是
1。正式环境记录的 LLM calls 为 0。

## 13. Pilot

Pilot 使用 5 个共同 seed、每 campaign 30 candidate；G0～G4 共 25 个 campaign、
750 条执行，另有 5 个 Legacy campaign、150 条执行，failure 为 0。

| 模式 | Raw | v2 | Election | Replication | Snapshot | Recovery | Network | Corpus |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Facet | 586.4 | 345.6 | 33.2 | 87.4 | 23.0 | 15.2 | 13.2 | 14.6 |
| Facet+Interaction | 559.0 | 336.0 | 33.2 | 85.2 | 21.4 | 15.0 | 14.0 | 14.8 |
| Random | 533.8 | 320.8 | 27.4 | 82.6 | 24.0 | 13.8 | 12.6 | 30.0 |
| Raw | 558.4 | 338.4 | 27.4 | 83.6 | 24.4 | 14.4 | 12.6 | 19.6 |
| v2 | 580.8 | 346.2 | 29.2 | 90.2 | 23.8 | 14.4 | 13.0 | 17.2 |

Pilot 只用于确认所有模式能增长、不会爆炸，并冻结 energy=2、Plan cap=80、正式
candidate=60、Corpus cap=128、queue=256、worker=1、snapshot threshold=2 和
retain=1。上表不用于正式优越性结论。

## 14. 正式 seed 和预算

正式矩阵共 5 模式 × 10 seed = 50 个 campaign、3,000 条候选：

`720001, 720101, 720201, 720301, 720401, 720501, 720601, 720701, 720801, 720901`。

每个 campaign 固定 60 candidate，实际平均 Action 在 4,417～4,470 左右，差异来自
Plan 实际解析和终止，不是不同的 Action 上限。所有原始 JSONL 使用
`artifact_policy=all` 保存。环境为 Linux/amd64、2 CPU、Go 1.26.0。

## 15. Cross-coverage matrix

以下均为 10 seed 的 campaign 均值。Facet 总数和 Interaction 总数没有相同全集
分母，不能相加后解释成“总体覆盖率”。

| 指导模式 | Raw | v2 | Election | Replication | Snapshot | Recovery | Network | 四类 Interaction 计数之和 | Corpus | Semantic Trace |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Facet | **1,103.9** | **637.4** | 38.7 | **139.6** | **36.9** | **18.9** | **17.3** | 127.0 | 28.7 | 45.4 |
| Facet+Interaction | 1,011.1 | 594.6 | **40.2** | 134.8 | 36.0 | 18.6 | 16.8 | **127.5** | 28.2 | 44.9 |
| Random | 930.2 | 495.6 | 33.5 | 120.8 | 34.1 | 15.0 | 14.8 | 110.6 | 60.0 | 45.4 |
| Raw | 938.9 | 498.2 | 31.8 | 119.0 | 34.8 | 15.3 | 15.0 | 110.2 | 38.2 | **47.2** |
| v2 | 972.0 | 536.6 | 36.4 | 122.2 | 34.0 | 16.3 | 14.2 | 111.5 | 31.3 | 46.2 |

G3 在外部 Raw 和 v2 指标上也最高，排除了“只在自己的评价指标上获胜”的最简单
解释。G4 只有 Election 和 Interaction 总和略高，其他主要指标低于 G3。

## 16. Raw、v2、Facet 和 Interaction 增长

相对 Random，G3 的最终 Raw 增加 18.7%，v2 增加 28.6%；Raw 和 v2 的最后新覆盖
平均都接近第 60 个 candidate，说明完整状态空间在本预算内仍未饱和。

四种 Interaction 均值如下：

| 模式 | Election×Network | Replication×Network | Snapshot×Recovery | Recovery×Term | 合计（仅展示） |
|---|---:|---:|---:|---:|---:|
| Facet | 19.7 | 48.2 | 33.1 | 26.0 | 127.0 |
| Facet+Interaction | 20.0 | 49.4 | 33.5 | 24.6 | 127.5 |
| Random | 17.2 | 43.2 | 28.9 | 21.3 | 110.6 |
| Raw | 18.0 | 41.9 | 29.1 | 21.2 | 110.2 |
| v2 | 17.5 | 41.7 | 29.1 | 23.2 | 111.5 |

G3 没有用 Interaction 做准入，却在四类 Interaction 上都高于 Random。这是
Interaction 作为外部评价指标支持 G3 的证据。G4 相对 G3 的合计只多 0.5，不能证明
把 Interaction 加入在线准入有实际价值。

## 17. 每个 Facet 的结果

| Facet | Facet | Facet+Interaction | Random | Raw | v2 | G3 相对 Random |
|---|---:|---:|---:|---:|---:|---:|
| Election | 38.7 | **40.2** | 33.5 | 31.8 | 36.4 | +15.5% |
| Replication | **139.6** | 134.8 | 120.8 | 119.0 | 122.2 | +15.6% |
| Snapshot | **36.9** | 36.0 | 34.1 | 34.8 | 34.0 | +8.2% |
| Recovery | **18.9** | 18.6 | 15.0 | 15.3 | 16.3 | +26.0% |
| Network | **17.3** | 16.8 | 14.8 | 15.0 | 14.2 | +16.9% |

Snapshot 的增幅最小，Election 的最高值来自 G4。G3 的优势不是每个指标都最大，
但它在五个维度上都高于 Random，且没有用 Raw/v2/Interaction 直接做准入。

## 18. Coverage AUC

Action 归一化 AUC 越高，表示在相同 Action 进度上更早积累覆盖。

| 模式 | Raw AUC | v2 AUC | Election | Replication | Snapshot | Recovery | Network |
|---|---:|---:|---:|---:|---:|---:|---:|
| Facet | **593.71** | **357.57** | **29.72** | **96.08** | **28.35** | **15.09** | **13.79** |
| Facet+Interaction | 567.65 | 345.30 | 29.57 | 94.54 | 28.28 | 14.75 | 13.34 |
| Random | 552.93 | 323.25 | 28.01 | 90.86 | 28.26 | 13.69 | 13.16 |
| Raw | 561.70 | 326.18 | 27.39 | 90.71 | 28.31 | 13.74 | 13.00 |
| v2 | 579.68 | 338.82 | 28.63 | 92.57 | 28.20 | 13.97 | 12.61 |

G3 在七列都最高，但 Snapshot AUC 差距很小。AUC 支持“更早积累广度”，不代表
已经覆盖某个有固定分母的全集。

## 19. Saturation 趋势

Raw 和 v2 在所有正式 campaign 中都未被启发式规则判为近似饱和。G3 被判为近似
饱和的 campaign 比例分别为：

- Election 0.6；
- Replication 0；
- Snapshot 0.4；
- Recovery 0.7；
- Network 0.6。

Random 对应为 0.5、0.4、0.8、0.9、0.8。G3 尤其减轻了 Replication、Snapshot、
Recovery 和 Network 的早期饱和。

G3 的 Q4/Q1 为 Raw 0.738、v2 0.656、Election 0.247、Replication 0.271、
Snapshot 0.184、Recovery 0.059、Network 0.112；Random 对应为 0.464、0.304、
0.101、0.162、0.054、0.012、0.048。后四分之一仍有增量，但这些是增长趋势指标，
不是覆盖完整度百分比。

## 20. Corpus 大小和准入率

| 模式 | Corpus 均值 | 准入率 | 每次准入新增跨指标单元 | 多 Facet 准入比例 |
|---|---:|---:|---:|---:|
| Facet | 28.7 | 47.83% | **67.06** | **53.20%** |
| Facet+Interaction | 28.2 | 47.00% | 65.44 | 51.26% |
| Random | 60.0 | 100.00% | 29.24 | 22.17% |
| Raw | 38.2 | 63.67% | 46.05 | 32.45% |
| v2 | 31.3 | 52.17% | 56.73 | 43.06% |

Random 按定义接收每个成功且唯一的 Plan，所以 Corpus 等于 60；这不是“随机更完整”，
而是没有语义过滤。G3 用不到一半准入率保留了更高覆盖贡献的 Plan。

## 21. Corpus 语义重复率

Corpus 内重复使用冻结的 Semantic Trace digest，不使用精确 Trace hash：

| 模式 | 语义重复率 |
|---|---:|
| Facet | 0.33% |
| Facet+Interaction | 0.33% |
| Random | 24.33% |
| Raw | 2.52% |
| v2 | 0% |

G3 明显减少 Random 和 Raw Corpus 的语义重复，但没有优于 v2 的 0%。同时，所有
候选的 Semantic Trace 数均值为 G3 45.4、Random 45.4，说明 G3 的主要收益是同样
数量的语义 Trace 携带了更丰富的 coverage unit，而不是产生更多语义 Trace 类别。

G3 每 campaign 平均仍有 10.1 条“Raw 新但 Facet 旧”和 5.8 条“v2 新但 Facet
旧”，说明 Facet 确实会舍弃部分完整状态差异；这正是压缩重复的来源，也可能漏掉
未来对特定缺陷重要的细节。

## 22. Parent yield

| 模式 | 父 Plan 新颖子代 yield |
|---|---:|
| Facet | 82.15% |
| Facet+Interaction | 79.38% |
| Random | 81.43% |
| Raw | 84.29% |
| v2 | 81.92% |

G3 的 parent yield 与其他模式接近，不是最高。它的优势来自“更少、更高密度的
Corpus + 相近的父代产出”，不能写成“每个 Facet 父 Plan 都更容易产生新状态”。

## 23. Throughput 和开销

| 模式 | candidate/s | Action/s | model event/s | campaign 墙钟 ms |
|---|---:|---:|---:|---:|
| Facet | 2.47 | 181.84 | 186.63 | 24,335.9 |
| Facet+Interaction | **2.53** | **188.50** | **189.67** | **23,785.4** |
| Random | 1.99 | 147.89 | 146.28 | 30,350.2 |
| Raw | 2.17 | 160.24 | 157.54 | 27,865.7 |
| v2 | 2.46 | 183.40 | 183.56 | 24,446.0 |

G3 每 campaign 平均计时为 Raw projection 0.93 ms、v2 projection 460.90 ms、
Facet projection 1,154.99 ms、Corpus decision 108.14 ms。所有模式都 record-only
计算所有指标，因此这不是“启用 G3 相对不计算 Facet 的系统”的纯增量开销。

Random 更慢主要与接收全部 60 个 Corpus entry、执行和写入更大的 Corpus 路径有关。
本结果只能说明在本轮完整记录配置下 G3 没有降低吞吐，不能外推到不同硬件或关闭
artifact 的部署。

## 24. 离线 Goal A

Goal A 是 `snapshot-catchup-after-partition`，只在执行后离线评价，不参与搜索。

| 模式 | 达到目标的 campaign | 600 条 run 中目标命中 | W1 | W2 | W3 | W4 | 目标相关 Interaction |
|---|---:|---:|---:|---:|---:|---:|---:|
| Facet | 3/10 | 24 | 593 | 271 | 157 | 84 | 175 |
| Facet+Interaction | 3/10 | 15 | 594 | 273 | 153 | 81 | **194** |
| Random | **5/10** | 24 | 591 | 289 | 149 | 82 | 154 |
| Raw | 4/10 | 24 | 594 | **292** | 156 | 80 | 150 |
| v2 | 4/10 | 24 | **596** | 220 | 138 | 74 | 140 |

G3 有更多 W3 和目标相关 Interaction，但最终目标 run 数与 Random 相同，且命中
分散在更少 campaign 中。按 campaign 计，Random 反而更高。Facet 广度不能替代
Waypoint Frontier 或 focused mutation。

## 25. 离线 Goal B

Goal B 是 `restart-then-higher-term-message`。

| 模式 | 达到目标的 campaign | W1 | W2 | W3 | 目标相关 Interaction |
|---|---:|---:|---:|---:|---:|
| Facet | 0/10 | 593 | **107** | **18** | **219** |
| Facet+Interaction | 0/10 | 594 | **107** | 0 | 173 |
| Random | 0/10 | 591 | 83 | 0 | 193 |
| Raw | 0/10 | 594 | 92 | 0 | 180 |
| v2 | 0/10 | 596 | 94 | 0 | 198 |

G3 是固定矩阵中唯一产生 W3 轨迹的模式，共 18 条，但没有形成最终 Goal。这个结果
是深度推进的积极诊断信号，不是 Goal 成功，也不能用来声称缺陷检出提升。

Legacy 参考矩阵曾有 Goal B 1/10，但它不属于 fixed-energy 公平比较。

## 26. Quorum mutant

Quorum mutant 必须使用五节点配置；三节点下 `n/3+1` 与正常多数派都等于 2，不能
形成有效差异。五节点 control 的 25 个 campaign 全部为 0 failure，mutant 的五种
模式全部 5/5 检出。

所有 mutant campaign 都在第 1 个 candidate、累计 100 Action 内首次失败；每种模式
的 300 条候选中，295 条为 TLC `disabled_action BecomeLeader`，5 条由 Oracle 报告
`raft.basic:multiple_leaders_same_term`。这说明 mutant 可被检测，但也说明它过于
容易：guidance 尚未影响后续 Corpus 前，错误已经出现。

按当前决定，本轮没有执行 Quorum 代表 Trace replay 和 ddmin。报告不得把这两项写成
已完成。

## 27. Snapshot mutant

Snapshot status-invert mutant 的五种模式均为 5/5 检出，Wilson 95% 区间均为
[0.566, 1.000]。首次失败平均为 candidate 1.8、累计 139.4 Action，各模式完全相同；
平均墙钟为 577～612 ms。

失败层是 Mapper/TLA+ transition mismatch，主要签名是 snapshot status 的
`reject` 方向与 pending/match/next 变化不一致。因为失败普遍发生在前 1～3 个
candidate，无法比较后续 Corpus 指导。

## 28. Restart mutant

Restart lose-HardState mutant 的五种模式同样均为 5/5，Wilson 95% 区间为
[0.566, 1.000]。首次失败平均为 candidate 2.0、累计 167.8 Action，各模式相同；
平均墙钟为 731～800 ms。

检测层包括：

- Oracle `raft.basic:term_regressed`；
- restart 时 etcd-raft panic，例如
  `applied(3) is out of range [prevApplied(1), committed(1)]`。

多层检测说明系统不是只依赖一个 Oracle，但 mutant 仍然太早暴露，不能区分指导
方法。

## 29. Control false positive

三节点 control 供 Snapshot/Restart 共用，五节点 control 用于 Quorum。两个 control
矩阵各 25 个 campaign，总计 0/50 false positive；每种模式的 0/5 Wilson 95% 上界
仍为 0.434。

由于 `artifact_policy=failures`，control 的 1,500 条成功候选没有完整 run 目录。
可以确认 benchmark failure 为 0、决策重算 mismatch 为 0，但不能声称这些未保存
run 已做完整逐字段离线 Observation 比较。

## 30. Replay 和 ddmin

已完成的代表失败：

| Mutant | Trace replay | PlanAction 缩减 | 尝试 | 最终稳定复验 | one-minimal |
|---|---:|---:|---:|---:|---:|
| Snapshot | 74/74 matched | 74 → 14 | 117 | 3/3 | true |
| Restart | 89/89 matched | 90 → 25 | 331 | 3/3 | true |
| Quorum | 未运行 | 未运行 | — | — | — |

Snapshot 最小化保持同一 normalized mapping signature；Restart 保持同一 restart
panic signature。Replay 的 `status=completed` 只证明给定 Trace 的具体步骤可确定
匹配；完整 failure 的稳定重现由 minimizer 的独立重跑证明。

## 31. 统计分析

根目录 `benchmark-statistics.json` 保存每个 seed 的 mean、median、标准差、IQR、
min/max；到达率和检出率使用 Wilson 95% 区间。本文选择关键值展示，完整统计以
JSON 为准。

Raw/v2 的描述统计：

| 模式 | Raw mean / median / SD / IQR / min–max | v2 mean / median / SD / IQR / min–max |
|---|---|---|
| Facet | 1103.9 / 1089 / 190.6 / 285.8 / 818–1365 | 637.4 / 632.5 / 99.6 / 182.0 / 526–778 |
| Facet+Interaction | 1011.1 / 969.5 / 133.7 / 107.3 / 818–1294 | 594.6 / 596.0 / 62.5 / 102.3 / 482–672 |
| Random | 930.2 / 938.5 / 118.7 / 198.8 / 765–1100 | 495.6 / 502.0 / 54.6 / 70.3 / 383–571 |
| Raw | 938.9 / 891.0 / 184.1 / 258.3 / 725–1307 | 498.2 / 501.0 / 59.1 / 92.5 / 402–574 |
| v2 | 972.0 / 953.0 / 174.1 / 202.3 / 765–1259 | 536.6 / 536.5 / 72.7 / 81.5 / 401–663 |

G3 相对 Random 的 Cliff's delta 为 Raw 0.54、v2 0.80、Election 0.27、
Replication 0.50、Snapshot 0.19、Recovery 0.41、Network 0.42。它们是描述性效应
大小，不是显著性证明。正式样本只有 10 个 seed，按预设规则不宣称统计显著。

Goal A 的 G3 为 3/10，Wilson 95% [0.108, 0.603]；Random 为 5/10，
[0.237, 0.763]，区间明显重叠。未发现 Goal 或 Bug 的运行按 budget-exhausted/
censored 保存，没有伪造首次到达时间。

## 32. 正面结果

1. 在线/离线共享 projection，在全部可比较正式 artifact 上 mismatch=0。
2. G3 同时提高自身 Facet、外部 Raw/v2 和未用于准入的 Interaction。
3. 五个 Facet 都高于 Random，稀有的 Recovery 和 Network 增幅也不是零。
4. G3 Corpus 更小，语义重复明显更少，每次准入的覆盖密度更高。
5. G3 的 Raw/v2 和多数 Facet AUC 更高，后期仍持续发现新行为。
6. 在完整记录所有指标的配置下，G3 吞吐没有下降。
7. G3 自然产生了其他固定模式没有出现的 Goal B W3 轨迹。
8. 三类 mutant 均能被 Runtime、Mapper/TLC 或 Oracle 检测，control 没有误报。
9. Snapshot 和 Restart 代表失败可重放、可稳定最小化。

## 33. 负面结果

1. G4 没有稳定优于 G3，Interaction 在线准入的额外复杂度暂时没有收益。
2. G3 的 Semantic Trace 数没有高于 Random，只是每条 Trace 的覆盖内容更丰富。
3. G3 parent yield 不是最高，不能把收益归因于“每个父 Plan 更好变异”。
4. Goal A campaign 到达率低于 Random，Goal B 最终到达仍是 0。
5. 三类 mutant 对所有方法都是 5/5，不能证明 G3 提高检出率或首次检出速度。
6. Quorum mutant 过于容易，几乎只验证检测链是否接通。
7. Snapshot AUC 和最终 distinct 的优势较小。
8. Legacy 多项指标更高，但设计不公平，当前不能解释原因。
9. 样本量只有 10 个正式 seed 和每模式 5 个 mutant seed，不能宣称显著。
10. Quorum 代表 failure 的 replay/ddmin 尚未运行。

## 34. Facet 是否适合在线引导

在当前三节点 etcd-raft、冻结随机变异器和 60-candidate 预算内，答案是“适合”。
关键依据不是 G3 在自己的 Facet 指标上高，而是：

- 外部 Raw 和 v2 同时提高；
- 未参与 G3 准入的四类 Interaction 同时提高；
- Corpus 语义重复率大幅下降；
- Action AUC 和后期新颖性提高；
- 吞吐没有受损。

因此推荐保留 `facet-fixed` 作为显式的在线全局广度模式。证据还不足以把它改成所有
普通 fuzz 的默认行为。

## 35. Facet 是否只适合离线评价

本轮结果不支持“只能离线评价”。G3 已实际改变 Corpus，并产生可观察的跨指标收益。
但以下内容仍应只做离线评价：

- 五个 Facet 的完整增长曲线和稀有单元；
- 四类 Interaction；
- Goal/Waypoint；
- Raw/v2 交叉指标；
- failure 前上下文和后续 LLM 输入。

特别是 Interaction 和 Goal 暂时不应接管在线准入：前者在 G4 中没有增益，后者属于
深度目标，不应与全局广度混为一个分数。

## 36. 当前限制

- 主要正式结果只来自三节点 etcd-raft，不代表其他共识协议；
- Quorum 使用五节点只是为了让 mutant 有效，不是五节点正式泛化实验；
- 只有一个随机变异器、一个预算档和 10 个正式 seed；
- Facet 没有固定全集分母，distinct 是相对实验广度，不是完成百分比；
- Raw/v2/Facet 虽是交叉指标，仍来自同一 TLA+ 映射和同一 Trace；
- mutant 是人工缺陷，不是生产 issue；
- mutant 太容易，无法比较指导方法的缺陷发现能力；
- `artifact_policy=failures` 限制了 control 成功 run 的完整离线复算；
- 没有测内存峰值；
- 没有运行 LLM，不能评价“LLM 告诉系统如何测试”的贡献；
- Quorum replay/ddmin 按用户决定跳过。

## 37. 下一阶段建议

本轮选择方向 A，并冻结以下边界：

1. 用 `facet-fixed` 管理普通 fuzz 的全局 Corpus 广度；
2. 保留五个独立 Facet，不重新拼成完整状态；
3. Interaction、Goal 和 Waypoint 继续完整记录，但不进入第一版在线准入；
4. 不采用 G4 作为默认方案；
5. 先设计更有区分力的历史 bug/较深 mutant，使错误不会在初始候选就暴露；
6. 再研究 `Facet Global Corpus + Waypoint Frontier + Focused Mutation` 的广度与
   深度分工；
7. LLM 应读取当前状态、未覆盖 Facet、Goal/Waypoint 缺口和候选历史，负责代码规则
   难以完成的多步测试规划，而不是代替 coverage 计数或合法动作检查；
8. LLM 实验必须单独设置无 LLM、随机和规则基线，并记录调用延迟、token、命中率和
   单位时间缺陷发现能力。

这些是下一轮建议，不是已经验证的结果。本轮不自动进入 LLM 实验。

## 38. Artifact 和复现命令

主要 artifact：

- Pilot：`runs/facet-guidance-pilot-20260729`
- Legacy Pilot：`runs/facet-guidance-legacy-pilot-20260729`
- 正式 G0～G4：`runs/facet-guidance-formal-20260729`
- Legacy 正式参考：`runs/facet-guidance-legacy-formal-20260729`
- 三节点 control：`runs/facet-guidance-mutant-control-20260729`
- Snapshot mutant：`runs/facet-guidance-mutant-snapshot-20260729`
- Restart mutant：`runs/facet-guidance-mutant-restart-20260729`
- 五节点 Quorum control：`runs/facet-guidance-mutant-quorum-control-20260729`
- Quorum mutant：`runs/facet-guidance-mutant-quorum-20260729`

正式矩阵重跑：

```bash
.tmp/modelfuzz-ng-facet-guidance-final coverage-benchmark \
  -manifest examples/facet-guidance-formal.json \
  -output runs/facet-guidance-formal-<date>
```

单 campaign 从原始 JSONL 重算：

```bash
.tmp/modelfuzz-ng-facet-guidance-final coverage-summarize \
  -input runs/facet-guidance-formal-20260729/facet-fixed/seed-720001
```

正式根目录中的 `manifest.json`、`seed-manifest.json`、`environment.json`、
`cross-coverage-matrix.csv`、`benchmark-statistics.json` 和
`online-offline-consistency-summary.json` 分别冻结配置、seed、环境、逐 seed
交叉矩阵、描述统计和一致性结果。

Snapshot 代表最小化报告：

`runs/facet-guidance-mutant-snapshot-20260729/facet-fixed/seed-730001/run-0000-seed-730001/minimized/minimization-report.json`

Restart 代表最小化报告：

`runs/facet-guidance-mutant-restart-20260729/facet-fixed/seed-730001/run-0002-seed-730003/minimized/minimization-report.json`

所有 TLC 服务只允许监听 localhost。当前报告没有补跑实验，也没有生成 Quorum
replay/minimized 目录。
