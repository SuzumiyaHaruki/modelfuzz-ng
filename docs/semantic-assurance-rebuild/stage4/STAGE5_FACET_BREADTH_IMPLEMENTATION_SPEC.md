# Stage 5 Facet Breadth Core v1 实现规格（GO）

## 1. 唯一目标

实现一个纯内存、只读输入、显式状态、确定性的 Facet Breadth Core：

```text
CandidateFacetSummaryV1
  -> CoverageState.Apply(ordinal)
  -> Novelty / Representative Update / Decision
```

Stage 4 真实 Pilot 已满足全部非退化门槛，因此 Stage 5 可以开始。Stage 5 不接入
Runner、Corpus、mutation、Artifact、checkpoint、CLI、Goal 或 Agent。

## 2. 推荐 package 与文件

推荐新增叶层 package：

```text
internal/assurance/facetbreadth/
  catalog.go
  summary.go
  state.go
  digest.go
  catalog_test.go
  summary_test.go
  state_test.go
  metamorphic_test.go
```

可合并文件，但生产文件不应超过 5 个。不得创建 registry、provider、observer、
plugin、store、writer 或 manager。

允许依赖：

- Go 标准库；
- `internal/assurance/facet`；
- `internal/assurance/executionrecord`。

禁止依赖：

- `runtime`、`engine`、`experiment`、`corpus`、`mutation`；
- adapters、`model/raft`、`model/tlc`、oracle；
- persistence、CLI、文件系统、网络、时钟或随机数。

## 3. 推荐公开类型

```text
CatalogIdentityV1
CatalogFacetIdentityV1
CandidateFacetSummaryV1
CandidateEvaluationSummaryV1
CandidateKeySummaryV1
RepresentativeRefV1
CoverageStateV1
CoverageSnapshotV1
DecisionV1
DecisionReason
```

推荐入口：

```go
func BuildCatalogIdentityV1(evaluators []facet.Evaluator) (CatalogIdentityV1, error)
func BuildCandidateSummaryV1(
    record executionrecord.CompletedExecutionRecordV1,
    evaluations []facet.EvaluationV1,
) (CandidateFacetSummaryV1, error)
func NewCoverageStateV1(catalog CatalogIdentityV1) (*CoverageStateV1, error)
func (s *CoverageStateV1) Apply(
    ordinal uint64,
    candidate CandidateFacetSummaryV1,
) (DecisionV1, error)
func (s *CoverageStateV1) Snapshot() CoverageSnapshotV1
func (s *CoverageStateV1) Digest() (string, error)
```

名称可以按仓库风格微调，但职责不得合并为自动 Runner/Corpus hook。

## 4. Catalog identity

`BuildCatalogIdentityV1` 只读取 evaluator 的 immutable `Definition()`，检查：

- 当前固定三 Facet；
- definition 合法；
- identity 不重复；
- class set 有限、排序、无重复；
- scope/version 与 Stage 4 contract 一致。

按 contract 计算每个 class-set digest 和 Catalog fingerprint。传入 evaluator 顺序、
实例 pointer 和 explanation 不影响结果。构建后不保留 evaluator。

## 5. Candidate summary

Builder 从 Stage 1 Record 和 Stage 3 evaluation 复制紧凑字段，不保存原对象。要求：

- 验证 Record schema、RecordDigest、Plan/Trace digest；
- 保留 candidate/run/plan/trace count；
- 保留三项 evaluation status；
- 对 evaluated observations 验证 Key/canonical/digest/occurrence；
- 按 Facet identity 和 key canonical string 排序；
- 去重只能接受完全相同的重复 observation；矛盾重复返回 error；
- 非 evaluated evaluation 必须没有 observations。

输入 slices、Key 和 occurrence 必须 defensive copy。调用方后续修改 Record 或
evaluation 不得改变 summary。

## 6. CoverageState 与 Apply

实现 `FACET_BREADTH_V1_CONTRACT.md` 的 eligibility、ordinal、First、Shortest、
Decision 和原子 Apply。

内部建议用 typed map 索引，但 commit 前先在局部 proposal 上完成全部校验。不可通过
“边遍历边写 State”实现。所有外部输出严格排序。

`Apply` 的 error 与 `ineligible_evidence` decision 必须分离：

- malformed schema/key/digest/Catalog/ordinal：error、State 不变；
- 完整但缺少严格 eligibility：successful decision，计数与 ordinal 前进；
- eligible 无 novelty：successful `no_novelty`。

## 7. Representative 与紧凑上界

`RepresentativeRefV1` 只含 contract 冻结字段。每个 key 最多 First/Shortest 两个
逻辑 slot；相同 ref 共享。不得保留全部历史 candidate。

当前 Catalog 31 keys，因此：

- covered key ≤31；
- logical representative slots ≤62；
- distinct compact ref ≤62；
- memory 随 apply candidate 数不得线性保存历史。

测试应执行至少 10,000 次 no-novelty Apply，证明 snapshot 中 key/ref 数不增长。
这不是 benchmark，不要求 wall-clock 指标。

## 8. Digest 与确定性

分别实现 Catalog、Candidate summary（若需要 identity）和 State 的 typed digest
payload。不要使用 `map[string]any` 作为主要结构。

测试必须验证：

- 相同输入重复至少 20 次 digest 相同；
- evaluator 传入顺序不影响 Catalog；
- evaluation/key 输入顺序不影响 summary；
- 同一 canonical Apply 序列产生相同 State snapshot/digest；
- ordinal、稳定 outcome、representative 变化会改变 StateDigest；
- debug explanation、pointer、调用时间不进入 digest。

## 9. 必须先写的测试

### Catalog

1. `TestCatalogIdentityV1FrozenRaftCatalog`
2. `TestCatalogIdentityV1IgnoresEvaluatorOrderAndPointer`
3. `TestCatalogIdentityV1RejectsDuplicateUnknownOrInvalidDefinition`
4. `TestCatalogClassSetDigestChangesWithClassOrVersion`

### Candidate summary

5. `TestBuildCandidateFacetSummaryV1`
6. `TestCandidateSummaryCanonicalizesEvaluationAndKeyOrder`
7. `TestCandidateSummaryRejectsCanonicalDigestMismatch`
8. `TestCandidateSummaryRejectsDuplicateMissingOrUnknownFacet`
9. `TestCandidateSummaryPreservesFirstOccurrenceOutsideKey`
10. `TestCandidateSummaryDefensiveCopies`

### Eligibility

11. Election/Replication evaluated + Snapshot evaluated；
12. Snapshot `not_applicable`；
13. invalid/insufficient；
14. missing/duplicate Catalog evaluation；
15. zero key；
16. failure status + valid Trace prefix remains eligible；
17. engine/oracle status is not reinterpreted。

### Apply / novelty

18. empty state first eligible candidate；
19. multiple new keys atomic insert；
20. no novelty；
21. ineligible successful decision；
22. candidate with new and existing keys；
23. invalid later key causes no partial mutation；
24. Catalog mismatch causes no mutation；
25. wrong/repeated/skipped ordinal causes no mutation；
26. counts and `NextApplyOrdinal` exact。

### Representatives

27. first immutable；
28. fewer Plan actions replaces shortest；
29. equal actions/fewer Trace steps；
30. PlanDigest tie-break；
31. TraceDigest tie-break；
32. RecordDigest tie-break；
33. exact equal no-op；
34. new key initial First==Shortest；
35. one candidate updates several shortest refs atomically；
36. `new_and_shorter` only when an existing key improves；
37. at most 62 logical slots and no candidate history。

### Determinism / ownership / race

38. snapshot and decision canonical ordering；
39. returned snapshot/result mutation does not change State；
40. input mutation after Apply does not change State；
41. 20 repeated operation sequences identical；
42. 10,000 no-novelty applies remain compact；
43. concurrent read-only Snapshot/Digest race-free；
44. serial Apply under `go test -race`；
45. two State instances do not share coverage。

### Stage 1–4 regression / feature off

46. executionrecord tests remain unchanged；
47. Facet 31 Golden fixtures remain unchanged；
48. real-trace Pilot remains unchanged；
49. no existing package imports facetbreadth；
50. constructing/applying breadth after completed evaluation does not modify Record/evaluation。

## 10. 验证

至少运行：

```text
go test ./internal/assurance/facetbreadth -count=1
go test ./internal/assurance/facetbreadth -count=20
go test -race ./internal/assurance/facetbreadth -count=1

go test ./internal/assurance/executionrecord -count=1
go test ./internal/assurance/facet/... -count=20
go test -race ./internal/assurance/facet/... -count=1

go test ./...
go test -race ./...
go vet ./...
gofmt -l internal/assurance/facetbreadth
git diff --check
```

并审计生产 import，确保 package 没有 Runtime/Engine/Experiment/Corpus 等依赖。

## 11. Acceptance criteria

Stage 5 只有同时满足以下条件才完成：

- 实现固定 schema 的 Catalog identity、Candidate summary、Coverage State；
- eligibility 与 contract 精确一致；
- Apply ordinal 严格递增；
- multi-key Apply 原子；
- First immutable；
- Shortest 使用五字段字典序；
- 五种 decision reason 封闭；
- compact state 上界成立；
- snapshot/digest/order 确定；
- defensive copy 与 race 通过；
- Stage 1–4 全部回归；
- 不修改 Frozen Kernel；
- 不接入 Runner、Corpus、mutation、Artifact、CLI、Goal 或 Agent。

## 12. Stop conditions

遇到以下任一情况必须停止：

- 需要修改 Engine、Experiment、Corpus、Runtime、Adapter、Mapper、Oracle；
- 需要让既有 package 反向 import facetbreadth；
- 需要读取 Trace/Artifact/文件系统才能 Apply summary；
- 需要保存完整 Record/Plan/Trace；
- 需要引入 registry/provider/plugin；
- contract 无法用原子 Apply 表达；
- Stage 4 real-trace Pilot 或 Stage 1–3 regression 失败；
- 必须改变 FacetKey、class catalog 或 evaluator 才能实现 Breadth。

不得借 Stage 5 顺便实现 artifact retention、checkpoint、Corpus admission、Goal 或
Agent。
