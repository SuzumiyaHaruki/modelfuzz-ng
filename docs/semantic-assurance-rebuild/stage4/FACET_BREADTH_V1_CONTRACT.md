# Facet Breadth v1 契约

## 1. 范围

本契约冻结一个纯内存、显式状态、确定性的 Facet Breadth Core。它消费已经完成的
`CandidateFacetSummaryV1`，维护当前 Catalog 的 FacetKey union 和每个 key 的有限
representative。

Breadth Core 不执行 candidate，不调用 Runtime/Mapper/TLC/Oracle，不读取 Artifact，
不修改现有 model Corpus，不选择 parent，不分配 energy，不执行 mutation，也不解释
Goal。本文中的 `Admitted` 只表示 candidate 进入 Facet Breadth representative set。

## 2. 固定 schema 与 identity

固定 schema：

```text
CandidateFacetSummaryV1: modelfuzz-ng-candidate-facet-summary-v1
CatalogIdentityV1:       modelfuzz-ng-facet-catalog-identity-v1
CoverageStateV1:         modelfuzz-ng-facet-breadth-state-v1
major_version:           1
```

所有 digest 均对规范化后的 typed payload 执行：

```text
encoding/json.Marshal -> SHA-256 -> 64 位小写十六进制
```

不得使用 map iteration、pointer、文件路径、wall-clock 或 evaluator 注册顺序建立
identity。

## 3. CandidateFacetSummaryV1

摘要只保存：

- schema/version；
- `RecordDigest`；
- Candidate ID、run index；
- `PlanDigest`、Plan action count；
- `TraceDigest`、Trace step count；
- 恰好三个、按 facet ID/version 排序的 evaluation summary；
- 每个 evaluation 的 `EvaluationStatus`；
- 每个 evaluated Facet 内按 canonical string 排序去重的 FacetKey canonical
  string、KeyDigest 和 first occurrence。

摘要不得内嵌 Completed Record、Plan、Trace、Observation、Model Event/State、
Finding 或 Artifact payload。

构建摘要时必须验证：

- Record/Plan/Trace digest 均为合法完整 SHA-256；
- count 非负；
- Facet identity、status、key 和 occurrence 自洽；
- canonical string 与 typed key、KeyDigest 一致；
- 同一 Facet 没有重复 key；
- 非 `evaluated` evaluation 没有 key；
- 所有 slice 已规范排序。

debug explanation 不进入摘要 identity。first occurrence 被保留用于证据定位，但不进入
FacetKey identity。

## 4. CatalogIdentityV1

Catalog identity 包含：

- schema/version；
- 按 facet ID/version 排序的三项 definition identity；
- 每项的 facet ID、version、scope；
- 按 class ID 排序的完整 class set；
- class-set digest；
- Catalog fingerprint。

class-set digest 的 typed payload 为：

```text
facet_id, facet_version, scope, sorted_class_ids
```

Catalog fingerprint 的 typed payload为：

```text
schema_id, major_version, sorted[
  facet_id, facet_version, scope, class_set_digest
]
```

当前 v1 Catalog 恰好包含：

- `raft.election_role_term_shape` v1，13 classes；
- `raft.replication_alignment_shape` v1，8 classes；
- `raft.snapshot_lifecycle_event` v1，10 classes。

Catalog identity 不依赖 evaluator pointer、传入顺序、源文件路径或 explanation。

## 5. Eligibility

一个 candidate 只有满足全部条件才可贡献 Coverage：

1. summary schema/version 与 State 相同；
2. Catalog fingerprint 与 State 完全相同；
3. 恰好存在三个 Catalog evaluation，且无重复/未知 Facet；
4. Election 为 `evaluated`；
5. Replication 为 `evaluated`；
6. Snapshot 为 `evaluated` 或 `not_applicable`；
7. 不存在 `invalid_evidence` 或 `insufficient_evidence`；
8. 总计至少一个 FacetKey；
9. 每个 key 属于当前 definition 的有限 class catalog；
10. canonical string、typed key 与 digest 完全一致。

Engine/Experiment status 不直接决定 eligibility。具有合法 Trace prefix 的失败
candidate 可以贡献 Facet coverage。Breadth Core 不重新分类 TLC/Oracle/Engine
failure。

结构合法但不满足第 4—8 项的 candidate 返回
`ineligible_evidence` decision；schema、identity、digest、排序、重复或 Catalog
不一致属于 error。error 时 State 字节级语义不变。

## 6. RepresentativeRefV1

Coverage State 不保存 candidate 大对象，只保存紧凑 ref：

- RecordDigest；
- Candidate ID；
- run index；
- PlanDigest；
- Plan action count；
- TraceDigest；
- Trace step count；
- apply ordinal。

ref identity 使用 `RecordDigest`；相同 RecordDigest 的其余字段必须一致，否则是
invalid input。

## 7. CoverageStateV1

State 显式持有：

- schema/version；
- `CatalogIdentityV1`；
- 已覆盖 FacetKey union；
- 每个 key 的 `FirstRepresentative`；
- 每个 key 的 `ShortestRepresentative`；
- successful Apply candidate count；
- eligible/ineligible count；
- decision reason count；
- `NextApplyOrdinal`；
- deterministic StateDigest。

key 与 representative 快照必须按 key canonical string 排序。内部可使用 map，但不得
暴露 mutable map/slice，也不得依赖 map iteration 输出。

Coverage 不生成 cross-Facet Cartesian key、统一 reward、energy、Goal score、
mutation 或现有 Corpus entry。

当前 Catalog 最大 31 个 key，因此最多 62 个逻辑 representative slot；
First/Shortest 相同则共享一个 compact ref，distinct ref 上界不超过 62。
不保留覆盖历史。

## 8. Apply ordinal

空 State 的 `NextApplyOrdinal=0`。每次 `Apply(summary, ordinal)` 必须满足：

```text
ordinal == NextApplyOrdinal
```

一个结构合法的 eligible 或 ineligible decision 都算 successful Apply：

- applied candidate count 加 1；
- 对应 eligible/ineligible count 加 1；
- decision reason count 加 1；
- `NextApplyOrdinal` 加 1。

validation error 不产生 decision，所有计数和 ordinal 均不变。跳号、重复 ordinal 和
乱序均为 error。

First 定义为第一个 successful eligible Apply 并覆盖该 key 的 candidate。Core 不读取
wall-clock completion time。并发上层必须持久化实际 Apply 顺序或串行提供确定顺序。

## 9. Shortest comparator

每个 key 只有一个 Shortest。比较严格采用以下字典序，越小越优：

1. Plan action count；
2. Trace step count；
3. PlanDigest；
4. TraceDigest；
5. RecordDigest。

不得使用 duration、wall-clock、Candidate ID、run completion time 或 Artifact path。
完全相同 ref 为幂等 no-op。

新 key 初始化时 First 和 Shortest 同时指向该 candidate；这不计作“替换已有
shortest”。后续任何 eligible candidate，只要含已有 key，均可按上述比较改进
Shortest，无论它是否含新 key。

## 10. 原子 Apply

Apply 必须按以下顺序：

1. 对完整 summary 做 schema/Catalog/digest/order/duplicate validation；
2. 判断 eligibility；
3. 对 eligible candidate 基于 pre-state 计算完整 new-key set；
4. 基于 pre-state 计算已有 key 的完整 shortest replacement set；
5. 构造完整 Decision；
6. 验证全部 proposal 与 post-state；
7. 一次性提交 union、representative 和计数。

一个 candidate 覆盖多个 key 时不可部分写入。任意一个 key、occurrence、identity 或
representative ref 非法时，State 完全不变。

## 11. DecisionV1

封闭 reason：

- `new_facet_class`
- `shorter_representative`
- `new_and_shorter`
- `no_novelty`
- `ineligible_evidence`

定义：

- `new_facet_class`：至少一个新 key，无已有 key shortest replacement；
- `shorter_representative`：无新 key，至少一个已有 key shortest replacement；
- `new_and_shorter`：既有新 key，又替换至少一个 pre-state 已有 key 的 shortest；
- `no_novelty`：eligible，但无新 key 和 shortest replacement；
- `ineligible_evidence`：结构有效但不满足 eligibility。

Decision 至少包含：

- ordinal；
- candidate ID、run index、RecordDigest；
- pre/post covered count；
- sorted new keys；
- sorted shortest-replacement keys；
- `Admitted`；
- reason。

前三种 reason 的 `Admitted=true`；后两种为 false。初始化新 key 时同时建立
First/Shortest，但不会仅因此把 reason 升级为 `new_and_shorter`。

## 12. State snapshot 与 digest

State snapshot 是 typed、defensive、canonical 视图。StateDigest payload 包含：

- schema/version；
- Catalog fingerprint；
- sorted covered keys；
- 每个 key 的 typed First/Shortest ref；
- applied/eligible/ineligible counts；
- canonical decision reason counts；
- NextApplyOrdinal。

StateDigest 不包含 debug text、pointer、内部 map 容量或调用时间。相同 initial
Catalog 与 Apply 序列必须产生相同 snapshot 和 digest。

## 13. 所有权和并发

- State 是调用方显式创建并 campaign-local 持有；
- 不使用 package global、singleton、`init` registry；
- summary 和 snapshot 输入/输出均 defensive copy；
- 多个只读 snapshot/digest 调用可以安全并发；
- `Apply` 由调用方串行拥有；v1 不在 State 内建立 scheduler 或后台 goroutine。

## 14. 非目标

v1 不定义：

- 与 `internal/corpus` 的映射；
- Corpus admission、parent、limit 或 energy；
- persistence、checkpoint、artifact retention；
- Goal、Waypoint、Frontier、Handoff；
- cross-Facet key；
- Agent proposal；
- Runner/CLI integration。

这些边界在 Stage 5 完成前不得被隐式引入。
