# Stage 6 Fairness Protocol

状态：**PRE-REGISTERED BEFORE STRICT TLC RESULTS**

基线：

- branch：`agent/semantic-assurance-rebuild-v1-stage4`
- HEAD：`d281e2f2fdaba85a8e45d227f6342f93849eb5e0`
- Stage 5：批准的未跟踪输入，不修改

## 1. 唯一比较变量

封闭模式：

1. `current-baseline`
2. `facet-only`

两个模式唯一不同点是：完成 candidate 是否允许产生 children。

- `current-baseline`：campaign-local `corpus.Corpus.Consider` 返回 retained；
- `facet-only`：campaign-local `facetbreadth.CoverageStateV1.Apply` 返回
  `Decision.Admitted=true`。

每个 candidate 在两个模式中都计算 baseline 和 Facet shadow。Shadow 不影响 active
decision。

不比较 hybrid、random-only、energy、parent ranking、Goal、Agent 或 LLM。

## 2. 固定 Campaign

正式 seeds：

```text
6601
6602
6603
```

每个 seed、每个 mode：

| 参数 | 固定值 |
|---|---:|
| candidate budget | 48 |
| initial population | 6 |
| mutations/admitted parent | 2 |
| Parallelism | 1 |
| MaxPlanActions | 40 |
| nodes | 3 |
| profile | storage-snapshot |
| MaxValue | 5 |
| MaxLogIndex | 10 |
| LargestTerm | 10 |
| snapshot threshold | 3 |
| retain entries | 1 |
| PreVote | false |
| CheckQuorum | false |

正式主比较为 `3 × 2 × 48 = 288` candidate 上限。重复性验证额外重跑 seed 6601
的两个 mode，不计入主比较指标。

Queue 提前耗尽时记录 `queue_exhausted`，不补种、不降门槛、不改变预算。

## 3. Initial population

每个 campaign seed 的六个 Plan 只生成一次，再 deep-copy 给两个 mode：

| Slot | Family |
|---:|---|
| 0 | `examples/plans/election.json` |
| 1 | `examples/plans/client-request-commit.json` |
| 2 | `examples/plans/follower-crash-restart.json` |
| 3 | production `policy.SnapshotPartition` success |
| 4 | production `policy.SnapshotPartition` with `FailFirstSnapshot` |
| 5 | production `policy.Random` |

Slots 3–5 使用真实 etcd-raft 执行和 recording `ActionSource` 生成高层
`plan.PlanSequence`；该记录过程不计入 A/B 指标。所有 Plan 在比较前 Validate。
不使用 Concrete Actions 冒充 Plan。

## 4. Production mutator 与固定 energy

两个模式使用同一个 `mutation.Random` 实例配置：

- NodeIDs `{1,2,3}`；
- MaxValue 5；
- MaxTicks 5；
- MaxActions 40；
- MaxCrashed 1；
- LifecycleCooldown 48；
- MaxCrashEpisodes 4；
- CrashRestartPairPercent 5；
- PartitionHealPairPercent 5。

每个 admitted parent 固定生成 child-0、child-1。每个 slot 单独调用 production
mutator，`Count=1`。不根据 raw state、semantic key 或 FacetKey 数量改变 child 数。

## 5. Lineage 与 seed

Lineage：

```text
initial/<slot>
<parent-lineage>/child-0
<parent-lineage>/child-1
```

使用 typed UTF-8 payload 的 SHA-256 派生：

- mutation seed：campaign seed + parent lineage + child slot；
- execution seed：campaign seed + candidate lineage。

取 digest 前 8 bytes 并清除符号位，得到确定性正 int64。seed 不依赖执行 ordinal、
mode、wall-clock、goroutine 或 map iteration。

Mutator 的 neutral parent `corpus.Entry.ID` 使用 parent lineage；两个 mode 中相同
lineage 的 parent Plan、entry metadata、mutation seed 和 child slot 完全一致。

## 6. 执行链与 strict TLC

每个 candidate：

```text
Plan
-> real runtime.Runtime
-> real adapters/etcdraft.Adapter
-> engine.Engine
-> real Raft Mapper
-> strict controlled TLC
-> real Raft Oracle
-> experiment.RunFeedback (Runs=1, Parallelism=1)
-> experiment.Completion
-> executionrecord.BuildV1
-> facet.EvaluateAll(..., raft.CatalogV1())
-> baseline/facet shadow
-> active admission
```

Runner 的 initializer/source 只由 lineage 决定，不包含 mode。configuration
fingerprint 对相同 campaign 的两个 mode 完全相同。PlanDigest、TraceDigest 和
ModelStatePathDigest 必须来自 RunFeedback，不手工伪造。

正式 TLC：

- model：`models/raft/raft_storage_snapshot.tla`
- cfg：`models/raft/raft-storage-snapshot-10.cfg`
- Server：`{1,2,3}`
- MaxValue：5
- MaxLogIndex：10
- LargestTerm：10
- Nil：0
- profile：`storage-snapshot`

未设置 `MODELFUZZ_STAGE6_TLC_URL` 时 gated formal test Skip；快速公平性测试使用
确定性 fake executor，不形成正式性能结论。

## 7. Baseline 与 Facet state

每个 mode 都拥有独立的：

- FIFO queue；
- `corpus.NewWithConfig({MinNewModelStates:1, RequireSemanticNovelty:true})`；
- `facetbreadth.CoverageStateV1`；
- lineage/result map；
- metrics accumulator。

Baseline shadow 使用 `model/raft.ProjectCoverage` 的 state/transition keys 和真实
ModelStates，并原样调用 `Corpus.Consider`。

Facet shadow 使用 BuildV1、CatalogV1、BuildCandidateSummaryV1 和严格递增 ordinal
Apply。

## 8. 固定指标

记录：

- execution/status/termination/finding counts；
- PlanAction、Concrete Action、Trace step、Model event/state totals；
- unique/duplicate PlanDigest、TraceDigest、ModelStatePathDigest；
- raw/semantic state/transition union 与 Corpus admission；
- Facet union、per-Facet count、decision reasons、representative lengths 和 first
  discovery ordinal；
- election/replication/snapshot classes；
- admitted parents、generated/executed children、depth、queue size；
- overlap/exclusive lineage；
- invalid/insufficient evidence。

Candidate 内 keys 去重后再计 coverage；不选择性报告对 Facet 有利的指标。

## 9. 重复性与公平性断言

seed 6601 的两个 mode 完整重跑一次。排除 wall-clock 和 TLC debug timing后，以下
必须一致：

- lineage sequence；
- per-lineage Plan/Trace/ModelStatePath digest；
- active admission 与 Facet decision；
- children；
- coverage sets；
- final metrics；
- Facet StateDigest；
- Corpus snapshot；
- final queue。

跨 mode 的重叠 lineage 必须具有相同 Plan、execution seed、PlanDigest、Engine
status、TraceDigest、ModelStatePathDigest、Oracle codes、Facet evaluations 和
baseline projection。

## 10. 机制 GO

必须全部满足：

1. strict TLC 正式 A/B 完成；
2. initial population 完全相同；
3.相同 lineage mutation 完全相同；
4.两个 mode 均执行 mutation child；
5.两个 mode 均有 admitted parent；
6.至少一个 campaign 在 initial population 后分叉；
7.重叠 lineage执行事实相同；
8. invalid/insufficient 均为 0；
9.无 harness 引入的 Runtime/TLC/Oracle failure；
10.预算、固定两 child、FIFO 和 Parallelism=1 成立；
11. seed 6601 重复性成立；
12.没有生产代码修改。

至少自然出现 `no_novelty` 或 shortest replacement；否则标记
`FACET_DECISION_DEGENERATE` 并暂停 Stage 7。

## 11. 性能方向

- `SIGNAL_POSITIVE`：至少部分预注册重复率/语义行为更好，且无明显损失；
- `SIGNAL_NEUTRAL`：差异很小或方向混合；
- `SIGNAL_NEGATIVE`：明显更早耗尽、重复更高或重要行为更少。

性能方向不改变机制 GO，也不允许回改参数。

## 12. Stop conditions

需要修改生产代码、Runner、Corpus、Facet、mutator、TLC/Mapper/Oracle，或无法保证
lineage mutation/overlap execution 等价，或 strict TLC profile/bounds 不匹配，或
必须改变 budget/energy/class 才能继续时立即停止。不得用 mutant、Goal、Agent、
不同 energy、额外 seed 或更大 budget 绕过。
