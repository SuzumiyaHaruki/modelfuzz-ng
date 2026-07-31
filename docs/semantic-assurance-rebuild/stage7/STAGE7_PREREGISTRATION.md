# Stage 7 Preregistration

状态：**FROZEN BEFORE ANY STAGE 7 STRICT TLC CAMPAIGN**

基线：branch `agent/semantic-assurance-rebuild-v1-stage4`，HEAD
`d281e2f2fdaba85a8e45d227f6342f93849eb5e0`。

已知先验：Stage 6 机制 `GO`、性能 `SIGNAL_NEGATIVE`；current-baseline / facet-only
unique TraceDigest 合计 136 / 114，Facet-only 在 6602、6603 提前 queue
exhaustion。此先验不允许在结果后修改 Facet v1。

## 1. Frozen implementations

本评价冻结并只读使用：

- `CompletedExecutionRecordV1`；
- Facet Core v1；
- `raft.election_role_term_shape` v1（13 classes）；
- `raft.replication_alignment_shape` v1（8 classes）；
- `raft.snapshot_lifecycle_event` v1（10 classes）；
- Facet Breadth Core v1；
- current `model/raft.ProjectCoverage` 与 `corpus.Corpus`；
- production `mutation.Random`；
- real etcd-raft、Raft Mapper、strict controlled TLC、Raft Oracle。

不修改 production、Stage 0—6、Facet class/key/catalog/eligibility/comparator。没有
hybrid、Goal、Agent、LLM 或新 energy。

## 2. Modes and the single active variable

封闭 modes：

1. `current-baseline`：`Corpus.Consider` retained 决定 children；
2. `facet-only`：`CoverageStateV1.Apply` 的 `Decision.Admitted` 决定 children。

每个 candidate 同时计算 baseline 与 Facet shadow；shadow 不反向影响 active mode。
双方每个 admitted parent 固定两个 children，使用同一个 production mutator、FIFO、
Parallelism=1。相同 parent lineage、child slot 和 campaign seed 必须产生相同 child
Plan；相同 candidate lineage 必须使用相同 execution seed。

沿用 Stage 6 typed SHA-256 seed payload和 lineage：

```text
initial/<slot>
<parent-lineage>/child-0
<parent-lineage>/child-1
reseed/<ordinal>
```

执行 seed只由 campaign seed、lineage 和 `execute` purpose 得到；不使用 mode、全局
执行 ordinal、wall-clock、goroutine 或 map iteration。

## 3. Historical block

历史 seeds（已验证，排序）：

```text
720001, 720101, 720201, 720301, 720401,
720501, 720601, 720701, 720801, 720901
```

冻结参数：

- 10 seeds × 2 modes；
- candidate budget 60；
- initial population 4；
- MaxPlanActions 80；
- fixed children 2；
- FIFO-once、Parallelism 1；
- Corpus 128、ready queue 256；
- snapshot threshold 2、retain 1；
- storage-snapshot、3 nodes、MaxValue 5、MaxLogIndex 10、LargestTerm 10；
- queue 为空即停止，不补种。

旧报告没有保留四个 initial Plan 的逐槽生成规则/digest。Stage 7 使用当前 production
`policy.Random` 生成四个 neutral initial Plan：每个 seed 的 slot seed只由
campaign seed与 `historical-initial/<slot>` 派生，生成一次后 deep-copy 给两个
mode。该 block 标记 `HISTORICAL_CONFIGURATION_REPLICATION_ONLY`，不称为逐候选
exact replication，并预先把最终最高结论限制为 `PARTIAL`。

## 4. Held-out seeds

公式：

```text
SHA-256("modelfuzz-ng-facet-v1-heldout-20260730:" + decimal_index)
index = 0..19
seed = big-endian(first 8 bytes) with sign bit cleared
```

冻结 literal list：

```text
8336454817672404382, 1032517847817231170,
1286863238667992368, 4120408576676485977,
1730231285957079546, 7480122269411795644,
1084788547682977977, 9054400645830646887,
418473828227667117, 6566132269545533901,
4936707319672310945, 825097919939804635,
8787426522159646979, 4384378045862814132,
3750190362025633128, 3961371642060324897,
5552216599351421507, 7687411833197131243,
7192736814863423931, 1235846154754625999
```

它们与 historical 和 `6601..6603` 不重叠；运行前在当前仓库和 sibling tracked
Markdown/JSON/CSV/CFG 中搜索，无已有命中。机器列表为
`results/heldout-seeds.json`。

## 5. Held-out Track A: closed tree

- 20 seeds × 2 modes；
- candidate budget 64；
- initial population为 Stage 6 相同六-slot family；
- MaxPlanActions 40；
- mutations/admitted parent 2；
- queue 为空即停止，不补种；
- FIFO、Parallelism 1。

## 6. Held-out Track B: neutral reseed

- 相同 20 seeds × 2 modes；
- candidate budget 128；
- 与 Track A 完全相同、同 seed逐项 digest相同的六-slot initial population；
- queue 非空时禁止 reseed；
- queue 为空且 executed<128 时生成恰好一个 `reseed/<ordinal>`；
- reseed 使用 production `policy.Random`，seed只由 campaign seed和 reseed ordinal
  派生；
- 两种 mode 的第 k 个 reseed Plan必须完全相同；
- reseed计入同一 candidate budget，不增加总预算；
- fixed children 2、FIFO、Parallelism 1。

初始六槽已经定向覆盖若干常见 Snapshot classes，这是 frozen 设计限制。Track B
不覆盖、替代或合并 Track A。

## 7. Common correct-implementation configuration

- nodes 3；
- storage-snapshot profile；
- MaxValue 5、MaxLogIndex 10、LargestTerm 10、Nil 0；
- snapshot threshold 3、retain 1（historical block例外为 2/1）；
- PreVote false、CheckQuorum false；
- MaxPlanActions 40（historical block例外为 80）；
- correct default fault policy；
- strict TLC；
- Raft Oracle；
- production `mutation.Random`；
- mutations/admitted parent 2；
- FIFO、Parallelism 1。

## 8. Mutant block

冻结 mutant：

```text
raft.faults.snapshot_status_mapping = "invert"
```

使用 held-out list 前 10 seeds、两种 modes、neutral reseed、candidate budget 128。
Initial population为六个 production `policy.Random` Plan；slot seed只由 campaign seed
与 slot确定，生成一次后 deep-copy 给两个 mode。禁止 directed Snapshot initial。

检测记录：

- first failing candidate ordinal（0-based；未检出右删失值 129）；
- first failing Concrete Action ordinal；
- lineage、PlanDigest；
- `minimize.SignatureOf`；
- success rate；
- minimized Plan action count。

不因未检出扩大预算。

## 9. Primary metrics

Confirmatory：

- P1 candidate supply：executed、queue exhaustion、exhaustion ordinal；
- P2 concrete diversity：unique TraceDigest、unique/executed、duplicate ratio；
- P3 rare classes：
  `snapshot_fast_forwarded`、`snapshot_rejected_or_stale`、
  `snapshot_status_ignored`，记录 reached与 first ordinal；未达到记 budget+1；
- mutant detection：success、first candidate/Concrete Action ordinal。

Supporting/exploratory：

- raw model state、semantic state、semantic transition、ModelStatePathDigest；
- per-Facet classes、Decision reasons；
- First/Shortest PlanAction与Trace step count；
- unique PlanDigest与重复率；
- active parent、child、depth、queue；
- model-bound、status、Oracle/failure；
- invalid/insufficient；
- replay、state/Corpus digest、TLC request。

不删除负面 seed或指标。

## 10. Paired statistics

按 seed配对。Deterministic paired bootstrap：

- bootstrap seed `7070707`；
- resamples `10,000`；
- 对 paired differences有放回重采样；
- 报告 mean difference、median difference、95% percentile interval；
- 报告 Facet better/equal/worse pair counts。

比例同时报告 absolute counts、paired binary difference与bootstrap interval。不做
未预注册的 p-value搜索。P1/P2/P3/mutant detection为 confirmatory；P4/P5为
supporting/exploratory。Historical只报告方向性，不作强统计复刻结论。

## 11. Repeatability and fairness

- Track A和Track B各对 held-out index 0 的两个 modes完整重复一次；
- mutant index 0 两个 modes重复第一次 failure身份；
- 比较 lineage序列、Plan/Trace/model-path digest、active admission、Decision、
  children、coverage、state/Corpus digest、final queue；
- 所有 mode overlap lineage必须逐项执行等价；
- invalid/insufficient必须为 0；
- strict TLC bounds必须完全匹配。

任何失败属于 infrastructure stop，而非性能结果。

## 12. Performance checkpoints

Closed-tree完成后，若同时满足：

1. Facet-only 至少 14/20 pairs更早 queue exhaustion；
2. unique TraceDigest ratio 的 paired median F/B <=0.85；
3. rare class reached count不高于 baseline；

则标记 `CONFIRMED_NEGATIVE`，不扩大 Track A；仍执行 Track B和mutant。

Track B若同时满足：

1. Facet-only 至少14/20 pairs unique Trace ratio更低；
2. rare reached不更高；
3. model-semantic breadth多数 pair更低；
4. mutant detection不更好；

则 active facet-only superiority判定不成立，最终为 `PARTIAL`，不修改 Facet或设计
hybrid。

## 13. Replay and minimize selection

Track B完成后，分别对：

- current-baseline shadow；
- facet-only active；

跨20 seeds为每个 observed FacetKey选取五字段 comparator下的全局 Shortest；按
RecordDigest去重，合计最多62个。每个执行 concrete Trace replay、相同 Plan strict
重执行、Oracle和Facet重算，并验证原 key与 Plan/Trace/model identity。

Mutant中每个 mode选择最早稳定 failure。先重复确认 signature，再使用现有
`minimize.Reduce`（MaxAttempts 1000、VerifyRuns 2、FinalVerifyRuns 3），final replay
最小 Plan并记录 one-minimal/cache/attempts。仅紧凑 minimized Plan可写入
`results/minimized/`。

## 14. Final decision

由于 historical initial identity缺失，本次结论最高预注册为 `PARTIAL`。

- `GO` 的原始性能条件仍用于评价方向，但本次不会越过 historical evidence上限；
- `PARTIAL`：机制、evidence、determinism、strict TLC、replay/minimize成立，但 active
  指标混合/负面或 historical复刻不充分；
- `BLOCKED`：仅用于 fairness、evidence、determinism、strict TLC、replay/minimize、
  frozen semantics或基础设施失败。

性能负面不是 infrastructure BLOCKED。

## 15. Infrastructure stop

以下任一项立即停止正式评价并标记 BLOCKED：

- preregistration在结果后改变；
- seed overlap或两个 mode initial/reseed不同；
- overlap lineage执行不等价；
- strict TLC profile/bounds不匹配或请求失败；
- invalid/insufficient evidence；
- repeatability或digest不确定；
- replay mismatch；
- mutant signature/minimize不稳定；
- 需要修改 production、Stage 0—6或 frozen Facet v1；
- 需要 Goal、Agent、hybrid、mode-specific energy/budget解释结果。

不扩大任何预算，不替换 seed，不删除不利结果。
