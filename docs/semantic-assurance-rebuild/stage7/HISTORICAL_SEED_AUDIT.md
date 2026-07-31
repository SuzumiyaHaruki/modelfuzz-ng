# Stage 7 Historical Seed Audit

状态：`HISTORICAL_CONFIGURATION_REPLICATION_ONLY`

## 1. 只读来源

审计没有切换旧仓库分支、没有导入或复制旧实现。可验证来源为 sibling reference
`/home/test/Desktop/modelfuzz-ng-partition` 中的：

- `docs/facet-guided-corpus-and-breadth-evaluation.md`
- `docs/semantic-coverage-factorization.md`
- `docs/research-evidence-ledger.md`
- `examples/facet-guidance-formal.json`
- `examples/facet-guidance-pilot.json`
- `examples/facet-guidance-mutant-snapshot.json`
- `examples/facet-guidance-mutant-control.json`
- `examples/config-facet-guidance-control.json`
- `examples/config-facet-guidance-snapshot-mutant.json`

使用的审计命令限于 `rg`、`sed`、`git log` 和 `git show`。旧仓库保持只读。

## 2. 可验证的正式 seeds

旧正式广度矩阵明确冻结了 10 个共同 seeds：

```text
720001
720101
720201
720301
720401
720501
720601
720701
720801
720901
```

它们与 Stage 6 development seeds `6601, 6602, 6603` 不重叠。数量为 10，因此不触发
`HISTORICAL_UNDERPOWERED` 的 `<8` 条件；但新旧 Facet schema 和 candidate
初始化身份不同，不能把样本数充足写成逐候选 exact replay。

## 3. 可验证配置

| 字段 | 旧正式值 | 来源 |
|---|---:|---|
| candidate budget | 60 | `facet-guidance-formal.json:runs` |
| MaxPlanActions | 80 | `max_plan_actions` |
| initial population | 4 | `initial_population` |
| Parallelism | 1 | `parallelism` |
| fixed energy | 2 | `fixed_energy` |
| parent selection | `admission-fifo-once` | `fixed_parent_selection` |
| Corpus limit | 128 | `coverage_corpus_limit` |
| ready queue | 256 | `max_ready_candidates` |
| snapshot threshold | 2 | `snapshot_threshold` |
| retain entries | 1 | `snapshot_retain_entries` |
| profile | `storage-snapshot` | control config |
| nodes | 3 | control config |
| MaxValue / MaxLogIndex / LargestTerm | 5 / 10 / 10 | control config |
| fault policy | correct default | control config |
| strict TLC | local `tlc_address` | formal manifest |

报告进一步确认 G0—G4 使用相同 Runtime、Adapter、mutator、strict TLC、Oracle、
fixed energy 与 FIFO-once；Facet 旧正式模式为五个独立 Facet 任一新增即准入。

## 4. 无法验证的 exact-replication 输入

已跟踪的报告和 manifest 只记录 `initial_population=4`，没有冻结：

- 四个 initial Plan 的逐槽 family；
- initial Plan 的派生 seed payload；
- 四个 PlanDigest；
- queue 为空后的 exact replenishment seed identity；
- 旧 Candidate/Plan/Trace identity 与当前 Stage 1 Record 的映射。

旧报告说明 fixed matrix 的 parent 为 admission FIFO-once，但没有提供足以重建相同
candidate tree 的逐槽输入。Stage 7 不读取或迁移旧实现来填补该缺口。

因此 Stage 7 只允许做“配置保持的方向性 historical paired evaluation”：使用上述
10 seeds、60/80/4/2、FIFO-once、threshold 2/retain 1，并以当前 production
`policy.Random` 构造四个确定性 initial Plan。它不能称为旧 artifact 的逐候选 exact
replication，historical 单独不能支持 GO。最终结论上限预注册为 `PARTIAL`，除非存在
独立可验证的旧 initial digest 证据；本阶段不会在结果后寻找或替换输入。

## 5. 旧结果基线

旧正式矩阵报告的 G3 `facet-fixed` 相对 Random：

- Raw distinct 均值 1103.9 vs 930.2；
- v2 distinct 均值 637.4 vs 495.6；
- Corpus 均值 28.7 vs 60.0；
- 五个旧 Facet 均值分别为 Election 38.7、Replication 139.6、Snapshot 36.9、
  Recovery 18.9、Network 17.3；
- 旧 Snapshot status-invert mutant 五种模式均 5/5 检出，首次失败均值 candidate
  1.8 / Action 139.4；
- 代表 Snapshot failure replay 74/74，ddmin 74→14、117 attempts、3/3 stable、
  one-minimal。

这些数字只作为历史方向和兼容性背景。新 Catalog 的 31 个有限 class 不与旧 key
空间同构，不能比较旧 SHA、新 SHA 或 distinct 数的绝对大小。
