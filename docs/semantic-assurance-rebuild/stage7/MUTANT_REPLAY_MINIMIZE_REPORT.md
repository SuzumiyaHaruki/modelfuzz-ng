# Stage 7 Mutant, Replay and Minimize Report

## 1. Mutant 与执行边界

冻结 mutant：

```text
raft.faults.snapshot_status_mapping = "invert"
```

使用 held-out 前 10 seeds、两种 mode、budget 128、neutral reseed、六个
mode-neutral production Random initial Plan。Plan recording始终使用正常 fault
policy；`invert` 只在 candidate正式执行时生效，避免把 mutant泄漏到 Plan生成过程。

## 2. Detection 结果

| seed | baseline detected | baseline candidate/action | Facet detected | Facet candidate/action |
|---:|:---:|---:|:---:|---:|
| 418473828227667117 | no | 129/129 | yes | 41/30 |
| 1032517847817231170 | no | 129/129 | yes | 68/28 |
| 1084788547682977977 | no | 129/129 | yes | 65/27 |
| 1286863238667992368 | yes | 3/30 | yes | 3/30 |
| 1730231285957079546 | yes | 3/26 | yes | 3/26 |
| 4120408576676485977 | yes | 2/40 | yes | 2/40 |
| 6566132269545533901 | yes | 15/40 | yes | 9/40 |
| 7480122269411795644 | yes | 5/33 | yes | 5/33 |
| 8336454817672404382 | yes | 5/37 | yes | 5/37 |
| 9054400645830646887 | yes | 16/35 | yes | 76/31 |

未检出使用预注册右删失值129。Detection success为 baseline 7/10、facet-only 10/10；
paired binary difference均值 +0.3，95% bootstrap interval `[0.0, 0.6]`。Facet在
3 pair检测而baseline未检测，没有反向 pair。

First candidate ordinal的 paired difference（Facet minus baseline）：

- 4 pair更早、5相同、1更晚；
- mean -15.9、median 0；
- 95% interval `[-41.3, 10.1]`。

因此 detection success不低且有局部增益，但首次到达并非一致改善。

所有 mutant campaign均执行满128 candidates；invalid/insufficient为0。两种 mode的
unique TraceDigest均为1,280，说明该 block的检测差异来自 candidate内容，而不是供给
数量或重复 Trace 数量。

## 3. Representative replay

从20个 neutral-reseed campaign中，分别为 current-baseline shadow 与 facet-only
active 的每个 observed FacetKey选择全局五字段 Shortest：

1. PlanAction count；
2. Trace step count；
3. PlanDigest；
4. TraceDigest；
5. RecordDigest。

结果：

- selected key slots：46；
- distinct RecordDigest：33；
- concrete replay mismatch：0；
- strict re-execution / Plan/Trace/model identity mismatch：0；
- FacetKey recomputation mismatch：0。

每项均先执行 baseline concrete Trace replay，再以同一 Plan走 real
Runtime/etcd-raft/Mapper/strict TLC/Oracle/Facet。明细见
`results/representative-replay.json`。

## 4. Failure stability 与 minimize

两个 mode选中的最早全局稳定 failure相同：

- seed `4120408576676485977`；
- lineage `initial/2`；
- original PlanDigest
  `8789df64a89b6e22bb03c990db0935209bfd054aa373955d05c4bd49554d654a`；
- signature status `mapping_failed`；
- normalized mapping code为 snapshot status next/match不一致。

每个 mode独立执行现有 `minimize.Reduce`：

| mode | original | minimized | attempts | cache hits | stable verify | one-minimal |
|---|---:|---:|---:|---:|---:|:---:|
| current-baseline | 40 | 18 | 136 | 20 | 3 | yes |
| facet-only | 40 | 18 | 136 | 20 | 3 | yes |

两份 minimized Plan concrete replay均为 `completed`、matched steps 18；final strict
execution signature与原 signature一致。紧凑 Plan位于：

- `results/minimized/current-baseline-plan.json`；
- `results/minimized/facet-only-plan.json`。

没有修改 replay、minimize、Mapper、TLC 或 Oracle。

## 5. 判断

Mutant、representative replay和minimize的基础设施门禁全部通过。Mutant结果对
facet-only有正向 detection success信号，但不足以覆盖 historical可复刻性缺口，也
不足以把 mixed active-guidance结果升级为最终 GO。
