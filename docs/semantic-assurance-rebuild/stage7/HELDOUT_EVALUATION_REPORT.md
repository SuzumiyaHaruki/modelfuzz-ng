# Stage 7 Held-out Evaluation Report

## 1. 结论

20 个预注册 held-out seeds 的 closed-tree 与 neutral-reseed paired evaluation 均完成。
两条 Track 给出不同、但可同时成立的结果：

- **closed-tree 明显受 candidate supply 限制**：facet-only 20/20 queue
  exhaustion，baseline 15/20；Facet unique TraceDigest 合计少 175；
- **neutral-reseed 消除了 supply 数量差异，但没有提高 concrete trace
  diversity**：两方都执行 2,560 个 candidate，Facet unique TraceDigest 少 25；
- **neutral-reseed 下 Facet stream 的协议语义行为更广**：rare snapshot
  campaign-class presences 为 14 vs 3，raw/semantic/transition breadth 在 20/20
  pair 高于 baseline。

因此 Stage 6 的 closed-tree 负面先验得到支持，但主动 facet-only 并非在所有指标上
一致负面。结果是 mixed signal，不支持“facet-only 长期 admission 优于 current
baseline”的声明。

## 2. 固定执行条件

- seeds：`results/heldout-seeds.json` 中冻结的 20 个 int64；
- current-baseline / facet-only；
- 相同六-slot initial population、production mutator、FIFO、Parallelism 1；
- 每 admitted parent 两个 children；
- storage-snapshot、3 nodes、5/10/10、snapshot threshold 3、retain 1；
- strict TLC、Raft Oracle、正确 default fault policy；
- Track A budget 64，无补种；
- Track B budget 128，仅 queue empty 时用 mode-neutral production Random reseed。

Initial population 前置覆盖若干常见 Snapshot classes，这是冻结设计限制。

## 3. Aggregate primary metrics

### 3.1 Track A：closed tree

| 指标 | current-baseline | facet-only | paired result |
|---|---:|---:|---|
| executed | 848 | 698 | mean diff -7.5；median -4；CI `[-15.6, 0.7]` |
| queue exhausted | 15/20 | 20/20 | Facet supply 更弱 |
| unique TraceDigest | 848 | 673 | 6 better / 2 equal / 12 worse |
| unique Trace ratio | 1.000 | 0.964 | 0 / 14 / 6；mean diff -0.03095 |
| rare class presences | 1 | 0 | 0 / 19 / 1 |
| raw model states | 4,348 | 3,028 | 6 / 0 / 14 |
| semantic states | 3,968 | 2,885 | 6 / 0 / 14 |
| semantic transitions | 5,364 | 3,757 | 6 / 0 / 14 |

预注册 `CONFIRMED_NEGATIVE` 三联门槛没有机械触发：虽然 Facet 的 queue exhaustion
更多、rare 不高于 baseline，但 paired median trace-ratio difference 为 0，而不是
F/B 中位数低至 0.85。方向仍然清楚地不利于 closed-tree facet-only，但没有改写门槛。

### 3.2 Track B：neutral reseed

| 指标 | current-baseline | facet-only | paired result |
|---|---:|---:|---|
| executed | 2,560 | 2,560 | 20 pair 全相等 |
| queue exhausted | 0/20 | 0/20 | neutral reseed 保证固定预算 |
| unique TraceDigest | 2,560 | 2,535 | 0 better / 14 equal / 6 worse |
| unique Trace ratio | 1.000 | 0.9902 | mean diff -0.009766；CI `[-0.01758, -0.003125]` |
| rare class presences | 3 | 14 | 7 better / 11 equal / 2 worse |
| raw model states | 14,229 | 35,787 | 20 / 0 / 0 |
| semantic states | 11,851 | 30,978 | 20 / 0 / 0 |
| semantic transitions | 17,105 | 43,247 | 20 / 0 / 0 |
| unique model-state paths | 1,617 | 2,026 | 19 / 0 / 1 |

Rare class paired mean difference为 +0.55，95% interval `[0.05, 1.1]`。Facet stream
观察到全部三个预注册 rare classes；baseline stream观察到
`snapshot_fast_forwarded` 和 `snapshot_status_ignored`，未观察到
`snapshot_rejected_or_stale`。这是一项正向深层行为信号，但不能抵消 concrete
TraceDigest 略低的事实。

预注册 neutral futility 四联条件未触发：Trace ratio 对 Facet 不利，但 rare behavior、
model-semantic breadth 和 mutant detection 均不是“不更好”。

## 4. Per-seed paired table

缩写：`CE/CT` 为 closed executed/unique traces；`NT` 为 neutral unique traces；
`NR` 为 neutral rare-class count。每格顺序均为 baseline/facet。

| seed | CE | CT | NT | NR |
|---:|---:|---:|---:|---:|
| 418473828227667117 | 50/22 | 50/22 | 128/128 | 0/2 |
| 825097919939804635 | 64/30 | 64/30 | 128/128 | 0/0 |
| 1032517847817231170 | 18/22 | 18/22 | 128/128 | 0/0 |
| 1084788547682977977 | 28/30 | 28/30 | 128/128 | 0/1 |
| 1235846154754625999 | 64/38 | 64/31 | 128/121 | 0/0 |
| 1286863238667992368 | 30/30 | 30/30 | 128/128 | 0/0 |
| 1730231285957079546 | 22/42 | 22/42 | 128/128 | 0/2 |
| 3750190362025633128 | 64/24 | 64/23 | 128/127 | 0/3 |
| 3961371642060324897 | 38/50 | 38/50 | 128/128 | 0/0 |
| 4120408576676485977 | 44/36 | 44/36 | 128/128 | 0/2 |
| 4384378045862814132 | 64/46 | 64/42 | 128/124 | 0/0 |
| 4936707319672310945 | 36/32 | 36/32 | 128/128 | 0/0 |
| 5552216599351421507 | 36/36 | 36/30 | 128/122 | 2/0 |
| 6566132269545533901 | 32/52 | 32/48 | 128/124 | 0/0 |
| 7192736814863423931 | 40/36 | 40/36 | 128/128 | 0/0 |
| 7480122269411795644 | 30/30 | 30/30 | 128/128 | 0/2 |
| 7687411833197131243 | 64/38 | 64/38 | 128/128 | 1/0 |
| 8336454817672404382 | 22/48 | 22/45 | 128/125 | 0/2 |
| 8787426522159646979 | 58/36 | 58/36 | 128/128 | 0/0 |
| 9054400645830646887 | 44/20 | 44/20 | 128/128 | 0/0 |

完整 paired 数值和 bootstrap 输出位于
`results/paired-campaign-metrics.csv` 与 `results/aggregate-statistics.json`。

## 5. Facet behavior 与 representatives

Neutral-reseed 全局 observed class sets：

| mode | election | replication | snapshot |
|---|---:|---:|---:|
| current-baseline shadow | 9 | 4 | 9 |
| facet-only active | 10 | 4 | 10 |

Neutral decision totals：

| mode | new | shorter | new+shorter | no novelty | ineligible |
|---|---:|---:|---:|---:|---:|
| current-baseline shadow | 130 | 147 | 2 | 2,281 | 0 |
| facet-only active | 179 | 398 | 3 | 1,980 | 0 |

所有 candidate 的 invalid/insufficient evidence 均为 0。

Neutral per-campaign representative averages显示，Facet stream覆盖更多 keys，也保留更多
representative：First count 9.1 vs 6.6，Shortest count 11.0 vs 8.1。平均 shortest
PlanAction 为 19.65 vs 16.81；这不是同一 key 集上的质量优势，不能把更大的 Plan
直接解释为 comparator 退化。

## 6. Fairness、repeatability 与限制

- 两个 mode 的 initial Plan digest逐项一致；
- 同序 reseed 的 Plan digest一致；
- overlap lineage 的 Plan/Trace/model-state path/Oracle/Facet/projection一致；
- held-out index 0 的 closed/neutral完整重复通过；
- strict TLC requests全部成功；
- 没有 Runtime/Oracle/harness failure；
- 没有删除不利 seed或调整预算。

限制：

1. initial population对常见 Snapshot classes有前置覆盖；
2. neutral reseed回答“supply充分时的 admission方向”，不是长期 scheduler设计；
3. raw/semantic breadth 是 candidate stream结果，不是 FacetKey 自身的替代指标；
4. 20 pairs仍是有界确认性评价，不支持任意协议或任意预算外推。
