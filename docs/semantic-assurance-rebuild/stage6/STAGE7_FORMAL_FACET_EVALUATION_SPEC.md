# Stage 7：Facet v1 最终评价与冻结规格

状态：**允许实施，但必须携带 Stage 6 `SIGNAL_NEGATIVE`**

## 1. 唯一目标

用冻结 Facet v1、相同 strict TLC、相同预算和相同 production mutator，对
`current-baseline` 与 `facet-only` 做可重复的 historical + held-out 正式评价，并决定
Facet v1 为 GO、PARTIAL 或 BLOCKED。

本阶段不修改 Facet class、Key、Catalog、TLA+、Mapper、Oracle、Runtime、Corpus 或
mutator，不实现 Goal、Agent、hybrid guidance 或新的 energy。

## 2. 预注册设计

在运行任何 held-out seed 前冻结：

- historical replication seeds 及其来源；
- held-out seeds；
- campaign 数和顺序；
- candidate/PlanAction/runtime 总预算；
- initial population；
- `mutations_per_admitted_parent=2`、FIFO、Parallelism=1；
- lineage、mutation seed、execution seed 规则；
- storage-snapshot strict TLC profile/bounds；
- current Corpus config 与 Facet-only admission；
- 至少一个仓库已有人工 mutant；
- 统计量、置信区间、多重比较策略；
- GO/PARTIAL/BLOCKED 和提前停止规则。

Historical 与 held-out 必须物理/逻辑分离。看到 held-out 结果后不得修改 Facet v1
classes、bucket、eligibility 或 representative comparator。

## 3. 比较矩阵

每个 frozen seed 对两种 mode 使用：

- 完全相同的初始 Plan；
- 完全相同的 Runtime/SUT/TLC/Oracle 配置；
- 相同 candidate、Action 和 mutation budget；
- 相同 production mutator；
- 每 admitted parent 固定两个 child；
- 独立 campaign-local Corpus 和 Facet state；
- deterministic lineage seeds；
- overlap lineage 逐项等价检查。

至少包含：

1. 正确默认实现的 historical replication；
2. 正确默认实现的 held-out campaigns；
3. 一个已有人工 mutant 的相同 A/B campaigns。

不得为某一 mode 补种、扩大预算或使用不同 queue/energy。

## 4. 指标

主指标：

- unique TraceDigest 和 model-state-path；
- raw model states、semantic states、semantic transitions；
- per-Facet class 与 Facet union；
- candidate/Trace/model-path duplicate ratio；
- queue exhaustion 与 generation depth；
- First/Shortest representative Plan/Trace 长度；
- 预注册深层 snapshot/recovery behavior 首次到达；
- mutant first detection candidate/Action ordinal；
- Oracle/failure signature；
- replay success 与 minimize 后长度。

辅助指标：

- admission/Decision reason；
- active parents、children、final queue；
- model-bound termination；
- invalid/insufficient evidence；
- TLC request/failure；
- overlap/exclusive lineage。

每项同时报告 candidate-level presence、绝对值和适当归一化值，不以 raw occurrence
夸大 coverage。

## 5. 旧/新 Facet 兼容表

在运行 held-out 前建立人工审查表：

- 旧实验概念；
- Facet v1 `facet_id/version/class_id`；
- exact overlap；
- narrower/wider/no-equivalent；
- evidence 来源；
- 是否只属于 Goal/history。

旧实现仅作语义对照，不迁移代码、key 或 artifact schema。

## 6. Replay 与 Minimize

对每个新 failure 和预注册 representative：

- 使用 baseline replay；
- 验证 Plan/Trace/model/Oracle/Facet identity；
- 对 failure 使用现有 minimize；
- 验证 failure signature 稳定；
- 报告 replay mismatch 与 minimize 前后长度。

不得由 Facet 重新分类 TLC/Oracle failure。任何 replay mismatch 单独作为基础设施失败。

## 7. 统计与判定

按 seed 配对比较；报告每个 seed 的差值、跨 seed 中位数/均值和 bootstrap 置信区间
（方法、重采样次数与随机 seed 在预注册中冻结）。区分 confirmatory 与 exploratory
指标；不因方向不利而删除 seed。

Stage 6 的负面先验要求至少设置以下停止门槛：

- Facet-only 在 historical replication 中多数 seed 明显更早 queue exhaustion；
- unique Trace 或预注册深层行为在多数配对中显著降低；
- invalid/insufficient evidence 非零；
- overlap lineage 不等价；
- strict TLC/Oracle/harness failure；
- mutant detection 明显更晚或缺失；
- replay/minimize 不稳定。

判定：

- **GO**：机制、公平性、重复性全部成立，held-out 上至少核心语义广度/重复率/深层行为
  有一致正向证据，且 mutant detection、failure/replay 无明显损失；
- **PARTIAL**：机制成立但指标混合，只允许冻结 offline/diagnostic Facet，不允许宣称
  active guidance 优势；
- **BLOCKED**：公平性、证据有效性、重复性、strict TLC、replay/minimize 失败，或负面
  停止门槛触发。

`SIGNAL_NEGATIVE` 不预先决定 Stage 7 结论，但禁止将“机制可运行”写成性能提升。

## 8. 实现边界

优先继续 test/integration harness。若必须生产化，只能在人工批准的新阶段设计窄接口；
Stage 7 本身不得顺便重构 Runner/Corpus、增加 registry/plugin/provider、Artifact
platform、Goal 或 Agent。

必须停止而不是扩建：

- 需要修改 Frozen Kernel 或 Facet v1 语义；
- 需要 mode-specific mutator/energy/budget；
- 需要在结果后调整 held-out；
- 需要新 TLA+/Mapper/Oracle 语义；
- 需要 LLM/Agent 在线分类；
- 无法保证 overlap lineage 等价。

## 9. 最终产物

- 完整 preregistration；
- machine-readable paired result；
- historical replication report；
- held-out report；
- mutant detection/replay/minimize report；
- compatibility table；
- Facet v1 GO/PARTIAL/BLOCKED freeze report。
