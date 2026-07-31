# Stage 7 Historical Evaluation Report

## 1. 结论

Historical block 完成 10 个已验证旧 seed 的 paired evaluation，但只能认定为
`HISTORICAL_CONFIGURATION_REPLICATION_ONLY`，不能认定为逐 candidate exact
replication。旧 tracked report/config 保存了 seed、数值配置和主要结果，却没有保存
四个 initial Plan 的逐槽生成规则或 digest。该缺口在正式运行前已经写入预注册，并把
Stage 7 的最高结论限制为 `PARTIAL`。

本次方向是负面的：current-baseline / facet-only 共执行 566 / 418 个 candidate，
facet-only 在 8/10 campaign queue exhaustion，baseline 为 2/10；unique
TraceDigest 合计也是 566 / 418。

## 2. 只读历史来源

审计对象为 sibling reference `/home/test/Desktop/modelfuzz-ng-partition`，只使用
`git log`、`git show`、`rg` 和 tracked report/config/result。没有切换旧分支、复制
旧实现或迁移旧 key。

验证到的 seeds：

```text
720001, 720101, 720201, 720301, 720401,
720501, 720601, 720701, 720801, 720901
```

验证到的旧数值配置：

- candidate budget 60、initial population 4、MaxPlanActions 80；
- fixed energy/children 2、FIFO-once、Parallelism 1；
- Corpus 128、ready queue 256；
- storage-snapshot、3 nodes、MaxValue 5、MaxLogIndex 10、LargestTerm 10；
- snapshot threshold 2、retain entries 1、strict TLC。

Stage 7 保留这些数值配置，用当前 production `policy.Random` 为每个 seed 确定生成
四个 neutral initial Plan，再 deep-copy 给两个 mode。它复刻配置和 seed，不伪造缺失
的旧 Plan identity。

## 3. Paired 结果

| 指标 | current-baseline | facet-only | paired 方向 |
|---|---:|---:|---|
| executed candidates | 566 | 418 | Facet 0 better / 2 equal / 8 worse |
| queue exhaustion campaigns | 2 | 8 | facet-only 更早耗尽 |
| unique TraceDigest | 566 | 418 | mean diff -14.8；median -14 |
| unique model-state paths | 407 | 309 | 0 / 0 / 10 |
| raw model states | 9,580 | 7,866 | 3 / 0 / 7 |
| semantic states | 8,255 | 6,702 | 2 / 0 / 8 |
| semantic transitions | 11,613 | 9,438 | 2 / 0 / 8 |
| rare class campaign-presences | 18 | 14 | 2 / 3 / 5 |
| invalid / insufficient evidence | 0 / 0 | 0 / 0 | 无 evidence failure |

`unique TraceDigest` paired mean difference（Facet minus baseline）为 -14.8，95%
bootstrap percentile interval `[-21.4, -8.6]`。Historical 不作强统计复刻结论；
该 interval 只描述当前配置复刻的方向。

Facet decision 不是退化的：

- baseline stream shadow：28 `new_facet_class`、142
  `shorter_representative`、11 `new_and_shorter`、385 `no_novelty`；
- facet-only stream：28、153、12、225。

## 4. 旧/新语义兼容

人工兼容映射见 `OLD_NEW_FACET_COMPATIBILITY.md`。Election、replication 和单步
snapshot lifecycle 中存在可比较概念，但旧 multidimensional tuple、interaction 和
历史型 retry/recovery 概念不能与新 v1 key 强行一一对应。没有比较旧 SHA 与新 SHA。

## 5. Replication 判断与限制

结论为：**配置方向复刻完成，逐 candidate historical replication 不成立**。

限制：

1. 缺少旧 initial Plan identity；
2. 旧 artifact 没有保存可供新 Record/Facet pipeline 逐条 replay 的完整引用；
3. 旧/new class catalog 不是同一个 schema；
4. 当前结果不能证明旧实验中的任何具体 candidate 被重新执行。

因此 historical 证据支持“closed candidate tree 下 facet-only 更易耗尽”的方向，却
不能单独支撑 Facet v1 的最终 GO。
