# Facet Global Corpus、Waypoint Frontier 与局部协议变异的广度—深度组合

## 摘要与决策

本轮实现并评价了显式两阶段方法：

```text
Global Coverage Corpus
  -> deterministic Handoff
  -> Waypoint Frontier
  -> focused protocol-aware local mutation
```

三节点正式矩阵包含 6 种方法、2 个 Goal、10 个共同 seed，共 120 个
campaign。全部完成且没有 benchmark 编排失败。Facet Global（M5）在所有两阶段
方法中取得最高的全局和最终广度，但没有维持 Local-only（M1）的 Goal reach：
Goal A 为 3/10 对 10/10，Goal B 为 8/10 对 10/10。M5 也没有在深度上稳定超过
Random、Raw 或 v2 Handoff。

因此本轮选择方向 B：Facet Corpus 与 Waypoint+focused search 保持两个显式模式，
不把两阶段组合冻结为默认算法。组合实现和 artifact schema 保留，供需要“先广后深”
的显式实验和后续 Handoff 研究使用。

## 冻结边界与两阶段设计

本轮没有增加 Facet、Interaction、Goal、Waypoint、Branch、Evidence 或 focused
Advisor 阶段，也没有实现双队列、统一浮点分数、动态预算、Bandit、RL 或 LLM。

采用两阶段而不是
`alpha * FacetNovelty + beta * GoalProgress`，因为 Facet 是集合增量，Goal 是有序
Waypoint 与 staged Distance；统一分数会混合量纲并破坏消融解释。

全局阶段复用冻结的 fixed-energy、admission-fifo-once coverage guidance：

- Random：成功且 PlanKey 唯一即准入；
- Raw：出现新 Raw TLC fingerprint 时准入；
- v2：出现新 v2 state 时准入；
- Facet：五个独立 Facet 任一新增时准入；
- Interaction 在所有模式中只记录，不参与准入。

全局阶段不启用 Goal、Waypoint Frontier、focused Advisor 或 Branch/Evidence。
Corpus 完成后标记 `corpus_frozen=true`。局部阶段只使用冻结的 Goal evaluator、
Standard Frontier、staged Distance、prefix preservation、focused Advisor、
strict TLC 和 Oracle，不回写 Global Corpus，也不使用 Facet 调整 Frontier。

## Schema、Handoff 与确定性

协议无关 schema 为
`raft-breadth-depth-handoff-v1-prototype`，主要结构是
`BreadthDepthRun`、`GlobalPhaseResult`、`GlobalEntry`、`HandoffSeed`、
`HandoffSet`、`LocalPhaseResult` 和 `CombinedSummary`。Raft Goal、Facet 和
Interaction 的解释仍位于既有协议语义层。

Handoff 先过滤满足 Goal entry condition 且可重放的 Corpus entry，再按以下顺序
确定性选择：

1. `CompletedWaypointCount` 更高；
2. staged Distance 更小；
3. target 已到达；
4. 在完全相同的 Goal progress 层内，依次增加相对 Semantic Trace、Facet
   组合、相对 queue shape 和 binding role 多样性；
5. Plan prefix 更短；
6. StableKey。

相对多样性去除 NodeID、MessageID、绝对 term/index 和执行身份。若没有合格
entry，可以显式 fallback；本轮正式矩阵没有发生 fallback。

进入局部 Frontier 前，Handoff replay 核对 Trace、Action/Effect、完整
Observation digest、Goal progress、Facet、model event、MessageID 和 StableKey。
任一不一致都会拒绝该 seed，不能静默从头开始。

## 预算、Pilot 与正式配置

总预算固定为 90 candidate、16,200 Action、每个 Plan 最多 180 Action：

| 方法 | Global/Local candidate | Global guidance |
| --- | ---: | --- |
| M0 Facet-only | 90/0 | Facet |
| M1 Local-only | 0/90 | 无 |
| M2 Random→Local | 60/30 | Random |
| M3 Raw→Local | 60/30 | Raw |
| M4 v2→Local | 60/30 | v2 |
| M5 Facet→Local | 60/30 | Facet |

Pilot 使用 seed 9301–9305，两个 Goal 共 10 个 campaign：

| 比例/K | Goal A | Goal B | 合计 |
| --- | ---: | ---: | ---: |
| 30/60, K=4 | 5/5 | 3/5 | 8/10 |
| 45/45, K=4 | 5/5 | 4/5 | 9/10 |
| 60/30, K=4 | 5/5 | 4/5 | 9/10 |
| 60/30, K=1 | 5/5 | 4/5 | 9/10 |

K=4 共选择 40 个 Handoff seed，但局部 Frontier 容量 1 最终每 campaign 只保留
一个；reach 和成本没有超过 K=1。正式配置因此冻结 60/30、K=1：它与 45/45
的 Pilot reach 相同，同时给全局广度更多预算，且避免无效 Handoff 重放。

正式 seed 为 9501–9510。环境是三节点 `storage-snapshot` profile，
`MaxLogIndex=10`、`LargestTerm=10`、snapshot threshold=3、retain=1、
Go 1.26.4、etcd-raft v3.7.0（本地 `../raft` replace）、strict TLC 1.8.0。
记录的源码 revision 为 `75d4e51120b370acb880d003629f916da3f1a080`，
工作树为 modified。LLM calls 为 0。

## 正式广度结果

全局阶段的 seed 与执行在两个 Goal 间复用，所以下表每个方法只有一组均值。
Facet/Interaction 是各分量计数之和。

| 方法 | Global Raw | Global v2 | Global Facet | Global Interaction |
| --- | ---: | ---: | ---: | ---: |
| M0（90 Global） | 2376.0 | 1374.3 | 421.4 | 187.3 |
| M2 Random | 1324.8 | 734.9 | 301.8 | 154.1 |
| M3 Raw | 1395.4 | 811.2 | 319.2 | 153.7 |
| M4 v2 | 1453.2 | 855.5 | 326.1 | 156.7 |
| M5 Facet | **1614.5** | **949.5** | **349.8** | **165.2** |

M0 的绝对广度最高是因为全部 90 candidate 都给了全局阶段；它是功能边界参考，
不能与 60/30 方法按单一指标排名。相同 60-candidate 全局预算下，M5 在所有外部
广度指标上最高，包括未参与准入的 Interaction。

完整 campaign 的 Final 结果如下：

| Goal | 方法 | Raw | v2 | Facet 总数 | Interaction 总数 | Semantic Trace |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| A | M0 | 2376.0 | 1374.3 | 421.4 | 187.3 | 69.9 |
| A | M1 | 41.8 | 34.8 | 43.0 | 33.2 | 17.0 |
| A | M2 | 1328.9 | 738.5 | 303.0 | 154.4 | 49.1 |
| A | M3 | 1400.0 | 814.5 | 320.3 | 154.3 | 49.0 |
| A | M4 | 1456.4 | 857.5 | 327.4 | 157.1 | 50.6 |
| A | M5 | **1617.5** | **951.7** | **351.1** | **165.5** | **52.9** |
| B | M0 | 2376.0 | 1374.3 | 421.4 | 187.3 | 69.9 |
| B | M1 | 26.5 | 26.4 | 33.5 | 22.4 | 8.5 |
| B | M2 | 1333.1 | 742.4 | 306.7 | 154.7 | 50.4 |
| B | M3 | 1404.2 | 818.8 | 323.4 | 154.4 | 51.9 |
| B | M4 | 1461.6 | 863.0 | 330.5 | 157.9 | 51.9 |
| B | M5 | **1622.0** | **955.3** | **352.9** | **165.7** | **54.5** |

M5 对 M1 的 Final Raw、v2、各 Facet、各 Interaction 和 Semantic Trace 的
Cliff's delta 均为 1.0，即当前 10×10 样本中完全分离；总 Action 的 delta
也是 1.0，说明广度收益伴随明确成本。

所有方法都保留了全局 coverage union，`global_coverage_retained=120/120`。
局部阶段仍有新增覆盖，但两阶段方法的增量相对较小。例如 M5 的局部新增 Raw
均值为 Goal A 3.0、Goal B 7.5；M1 分别为 41.8、26.5。

## Goal reach、Waypoint 与成本

Goal reach 和 Wilson 95% 区间：

| 方法 | Goal A | Wilson 95% | Goal B | Wilson 95% |
| --- | ---: | --- | ---: | --- |
| M0 | 3/10 | [0.108, 0.603] | 1/10 | [0.018, 0.404] |
| M1 | **10/10** | [0.722, 1.000] | **10/10** | [0.722, 1.000] |
| M2 | 3/10 | [0.108, 0.603] | 9/10 | [0.596, 0.982] |
| M3 | **5/10** | [0.237, 0.763] | 8/10 | [0.490, 0.943] |
| M4 | 3/10 | [0.108, 0.603] | **9/10** | [0.596, 0.982] |
| M5 | 3/10 | [0.108, 0.603] | 8/10 | [0.490, 0.943] |

M0 的 reach 是对全局 Trace 的离线 Goal 评价，不是局部搜索成功。所有未到达运行
都标记为 `budget_exhausted/censored`，没有把预算上限填成虚假首次到达值。

Waypoint 到达数（Goal A 为 W1→W7，Goal B 为 W1→W6）：

| 方法 | Goal A | Goal B |
| --- | --- | --- |
| M0 | 10,10,9,8,7,3,3 | 10,10,3,3,3,1 |
| M1 | 10,10,10,10,10,10,10 | 10,10,10,10,10,10 |
| M2 | 10,10,8,8,8,3,3 | 10,10,9,9,9,9 |
| M3 | 10,10,6,6,6,5,5 | 10,10,9,9,8,8 |
| M4 | 10,10,6,6,6,4,3 | 10,10,10,9,9,9 |
| M5 | 9,9,6,5,5,4,3 | 10,10,9,9,9,8 |

成功运行的局部增量成本均值：

| 方法 | Goal A candidate/action/ms | Goal B candidate/action/ms |
| --- | ---: | ---: |
| M1 | 21.8 / 399.9 / 5000.1 | 12.9 / 151.7 / 2106.6 |
| M2 | 1.0 / 105.3 / 893.3 | 13.0 / 706.3 / 6189.0 |
| M3 | 1.6 / 188.6 / 1867.0 | 15.4 / 562.6 / 5641.4 |
| M4 | 2.0 / 236.3 / 2000.3 | 14.9 / 659.3 / 6358.4 |
| M5 | 1.7 / 197.7 / 1544.0 | 15.1 / 1011.5 / 8861.6 |

成功子样本不能单独代表总体成本：Goal A 的两阶段成功往往已经从较深 Handoff
开始，但 5–7 个未成功运行仍消耗全局预算。M5 的完整 campaign Action 均值为
Goal A 11,851.4、Goal B 10,827.0；M1 分别仅 399.9、151.7。M5 对 M1 的完整
Action Cliff's delta 为 1.0。

## Handoff 质量

| Guidance | Corpus 均值 | Goal A 最深初始 WP | Goal A W3 entry | Goal B 最深初始 WP | Goal B W3 entry |
| --- | ---: | ---: | ---: | ---: | ---: |
| Random | 60.0 | 5.0 | 8.0 | 2.0 | 0.1 |
| Raw | 32.7 | 4.6 | 6.0 | 1.9 | 0.1 |
| v2 | 30.9 | 4.3 | 5.0 | **2.1** | **1.4** |
| Facet | 29.4 | 4.4 | 5.6 | 2.0 | 0.1 |

每个正式两阶段 campaign 都选中并保留 1 个 seed，fallback 为 0。Global Corpus
到 Handoff 的压缩率均值为 Random 0.017、Raw 0.031、v2 0.032、Facet 0.034。
所有 entry 满足 entry condition 的比例接近或等于 100%，Handoff replay 成功率
均为 1.0。

Facet Handoff 没有在初始 Waypoint 或 W3 entry 上稳定优于其他 guidance。
Goal A 的 Random 初始进度更深，Goal B 的 v2 W3 entry 明显更多。这直接反驳了
“Facet Corpus 必然提供更适合 Goal 的 seed”。

当前冻结设计没有对每个未选择 entry 再运行一轮完整局部搜索，因此
`unselected_deeper_posterior` 明确记录为 `not_evaluated_by_frozen_design`。
可以证明未选择 entry 的初始 Goal progress 不优于确定性排序选中项，但不能推断
其反事实局部后验；这是本轮限制，不能填成 0。

## 成功路径集中与互补性

成功路径计数为“成功运行数 / exact Trace / 相对 Semantic Trace / Goal progress
path”：

| 方法 | Goal A | Goal B |
| --- | --- | --- |
| M1 | 10 / 10 / **1** / 10 | 10 / 10 / **1** / 9 |
| M2 | 3 / 3 / 3 / 1 | 9 / 9 / 9 / 9 |
| M3 | 5 / 5 / 5 / 2 | 8 / 8 / 8 / 8 |
| M4 | 3 / 3 / 3 / 2 | 9 / 9 / 9 / 9 |
| M5 | 3 / 3 / 3 / 2 | 8 / 8 / 8 / 8 |

因此 Facet Handoff 确实缓解了“成功相对语义 Trace 全部塌缩为一个”的现象：
M5 的每个成功运行都有不同相对 Semantic Trace，并来自不同 Handoff semantic
class。但它同时降低了成功数，不能表述为总体方法更好。Goal A 的成功 progress
path 仍只有 2 个，也没有优于 M3/M4。

互补指标显示 Facet novelty 与 Goal progress 大多并不同步：

- M5 每个全局 run 平均有 Goal A 26.6、Goal B 27.6 次
  `new Facet without Handoff progress`；
- `Handoff progress without new Facet` 在 M5 两个 Goal 均为 0；
- “最高 Facet novelty seed 同时是最深 Goal seed”仅 Goal A 1/10、Goal B 6/10；
- M5 局部阶段 `Goal progress without new Facet` 均值为 Goal A 0、Goal B 0.8；
- M5 局部新增 Facet 但 Goal 不推进均值为 Goal A 2.0、Goal B 2.8；
- 全部 120 个 campaign 的全局 coverage 都被 Final union 保留。

这些数据支持 Facet 与 Goal 是不同信号，但不支持当前单 seed Handoff 已把广度
转化为深度。

## 公平性、Online/Offline 与 Replay

M2–M5 使用相同局部候选/Action 上限、Standard Frontier 容量 1、staged
Distance、prefix preservation、focused Advisor 参数、Runtime、strict TLC、
Mapper 和 Oracle。Branch/Evidence online 均关闭，record-only 数据不改变执行。

正式阶段：

- 全局 3300/3300 candidate 成功，失败 0；
- Handoff replay 3894/3894 成功，Observation/MessageID mismatch 为 0；
- 局部前缀 replay 1804/1804 成功，prefix execution mismatch 为 0；
- 所有生成出的 1794 个局部 candidate 都执行 strict TLC，可执行失败为 0；
- Online/Offline mismatch 为 0；
- LLM calls 为 0。

M5/Goal A 的 seed 9501 有 30 次局部 mutation 尝试全部被合法性检查拒绝，因而
该 campaign 没有执行候选。实现已修正为把这种未到达运行标记为
`budget_exhausted/censored`。除这一个 seed 外，各组生成候选合法率为 100%；
M5/Goal A 汇总合法率为 185/(185+30)=86.05%。这反映 Handoff 状态与局部
Advisor 的兼容性问题，不是 TLC 漏执行。

## Control、Mutant、Replay 与 ddmin

回归使用 seed 9701–9705、每 run 30 candidate/4500 Action，只评价错误检测没有
被组合实现破坏，不把早期 mutant 当作方法区分指标：

| Campaign | Bug detect | Goal reach | 首次失败 |
| --- | ---: | ---: | --- |
| Snapshot control | 0/5 | 5/5 | 无 |
| Snapshot status invert | 5/5 | 0/5 | candidate 8,21,28,30,16 |
| Restart control | 0/5 | 5/5 | 无 |
| Restart lose HardState | 5/5 | 0/5 | 全部 candidate 4 / Action 36 |

两个 control 合计 0/10 false positive。Snapshot failure layer 为稳定的
`mapping_failed`，Restart 为 `oracle_failed: raft.basic:term_regressed`。
全部 403 次前缀 replay 成功，Online/Offline mismatch 为 0。

代表失败的独立审计：

- Snapshot Trace replay 21/21；ddmin 17→13 Action，42 次尝试，
  最终签名 3/3 稳定，one-minimal=true；
- Restart Trace replay 12/12；ddmin 11→4 Action，30 次尝试，
  最终签名 3/3 稳定，one-minimal=true。

## snapshot threshold=5 配置泛化

泛化矩阵只比较 M1 与 M5，seed 9601–9605，共 20 个 campaign，其他预算和
strict TLC 设置不变：

| 方法 | Goal A | Goal B | Final Raw A/B | Final v2 A/B |
| --- | ---: | ---: | ---: | ---: |
| M1 | 0/5 | **5/5** | 49.0 / 26.6 | 42.0 / 26.6 |
| M5 | **1/5** | 3/5 | 1621.0 / 1620.8 | 988.8 / 988.6 |

Goal A 中 M1 全部到 W3 后预算耗尽，M5 有 1 个运行到达 W7；这表明更高
snapshot threshold 下 Handoff 偶尔有帮助。Goal B 则再次由 M1 获胜。278/278
Handoff replay、691/691 prefix replay 成功，Online/Offline mismatch 为 0。
该 5-seed 小矩阵只证明机制可工作和结果分化，不能推出广泛配置泛化。

## 测试、静态检查与兼容性

最终执行并通过：

```text
go test ./...
go test -race ./...
go vet ./...
```

新增测试覆盖版本化 schema、预算拆分、M0/M1 边界、确定性 Handoff 排序、
Waypoint/Distance 优先、K=1/4、身份无关多样性、fallback、失效 replay 拒绝、
Goal/Facet/StableKey 一致、Global/Final coverage union、统计表生成、censored
归一化和历史 CLI 兼容。普通 fuzz、coverage-benchmark、goal-search、
goal-benchmark、Replay、ddmin、strict TLC 和 Oracle 的默认路径没有改变。

## 正面结果、负面结果与限制

正面结果：

- 两阶段隔离、冻结 Corpus、确定性 Handoff 和严格 replay 已实现并稳定；
- M5 在同样 60-candidate Global 预算下取得最高 Raw/v2/Facet/Interaction；
- Handoff 增加了成功相对语义路径多样性；
- control 0 false positive，两个 mutant 5/5，Replay/ddmin 稳定；
- threshold=5 下机制仍工作，且 Goal A 出现 1 个 M1 未达到的成功。

负面结果：

- M5 正式 Goal reach 明显低于 M1；
- M5 没有在两个 Goal 上稳定超过 Random、Raw 或 v2 Handoff；
- Facet novelty 大多不转化为 Handoff progress；
- 60/30 拆分显著稀释局部预算并增加总成本；
- 一个 M5/Goal A seed 的局部生成完全失效；
- 成功路径更分散，但成功数量减少；
- 当前 K=4 不增加有效保留 seed。

限制：

- n=10 正式 seed、n=5 泛化和 mutant，只报告 Wilson 与 effect size，不宣称显著性；
- 两个 Goal、单一三节点主配置和人工 mutant 不能代表生产缺陷；
- M0/M1 是功能边界，不适合单指标总排名；
- success-only action/time 有删失选择偏差；
- 未运行全部未选择 Handoff seed 的反事实局部后验；
- 没有五节点、retain 变化、第二协议或实时双队列结果；
- 工作树是 modified，revision 只标识基线提交，完整差异以当前工作树为准。

## 下一阶段建议

保留两个显式入口：

1. `facet-fixed`：普通全局协议广度；
2. `waypoint-frontier + raft-focused`：指定 Goal 深度。

两阶段 `breadth-depth-benchmark` 保留为研究功能，但不设为默认。若继续研究，只
建议有限修正 Handoff：先针对 seed 9501 的局部生成失配，以及未选择 seed 的小型
反事实后验进行诊断；没有证据时不修改 Facet、Goal 或 focused Advisor，也不进入
LLM 阶段。

## Artifact 与复现

主要目录：

- `.tmp/breadth-depth-formal`：120 个正式 campaign 和根级统计表；
- `.tmp/breadth-depth-generalization-threshold5`：20 个泛化 campaign；
- `.tmp/breadth-depth-control-mutant-regression`：control/mutant、Replay、ddmin；
- `.tmp/breadth-depth-pilot-summaries`：四组 Pilot 汇总；
- `.tmp/breadth-depth-pilot-*.tar.zst`：已校验的 Pilot 压缩包。

每个组合 campaign 保存 `breadth-depth-settings.json`、
`handoff-settings.json`、`handoff-candidates.jsonl`、
`handoff-selected.json`、`handoff-replay.jsonl`、
`local-phase-summary.json`、`combined-summary.json` 和 Final coverage growth。
它引用的 `_global/<method>/<seed>` 目录保存
`global-phase-summary.json`、`global-corpus-manifest.json`、
`global-corpus-entries.jsonl` 和 Global coverage growth；同一 Global 执行由两个
Goal 只读复用，不复制原始大文件。根目录保存 local waypoint growth、cross
matrix、handoff quality、successful path diversity、complementarity、
figure-ready 和含 mean/median/SD/IQR/min/max、Wilson、Cliff's delta 的统计
JSON。

复现命令：

```bash
.tmp/modelfuzz-ng breadth-depth-benchmark \
  -manifest examples/breadth-depth-formal.json \
  -output .tmp/breadth-depth-formal

.tmp/modelfuzz-ng breadth-depth-benchmark \
  -manifest examples/breadth-depth-generalization-threshold5.json \
  -output .tmp/breadth-depth-generalization-threshold5

.tmp/modelfuzz-ng goal-benchmark \
  -manifest examples/goal-benchmark-breadth-depth-control-mutant.json \
  -output .tmp/breadth-depth-control-mutant-regression
```

已有完整目录可使用 `-skip-completed=true` 重算根级汇总而不重复消耗 campaign
预算。所有 summary 的输入均保留为 JSON/JSONL/CSV，并通过 StableKey、manifest、
seed 和 environment artifact 连接到原始执行。
