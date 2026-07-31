# Stage 5 实现报告：Facet Breadth Core v1

## 1. 基线与结论

- 实际基线分支：`agent/semantic-assurance-rebuild-v1-stage4`
- 实际基线 commit：`d281e2f2fdaba85a8e45d227f6342f93849eb5e0`
- 基线更新依据：人工在开始 Stage 5 前明确要求使用当前新分支及 commit
- 结果：完成。未触发停止条件。

本阶段新增一个叶层、纯内存 package：

```text
internal/assurance/facetbreadth
    -> internal/assurance/facet
    -> internal/assurance/executionrecord
```

现有 package 没有反向 import `facetbreadth`。生产代码不依赖 Runtime、Engine、
Experiment、Corpus、Mutation、Policy、Adapter、Model、Oracle、Persistence、
Trace、Minimize 或 CLI。

## 2. 实际新增文件

生产文件（4 个，1,071 行）：

- `internal/assurance/facetbreadth/catalog.go`
- `internal/assurance/facetbreadth/summary.go`
- `internal/assurance/facetbreadth/state.go`
- `internal/assurance/facetbreadth/digest.go`

测试文件（6 个）：

- `internal/assurance/facetbreadth/catalog_test.go`
- `internal/assurance/facetbreadth/summary_test.go`
- `internal/assurance/facetbreadth/state_test.go`
- `internal/assurance/facetbreadth/metamorphic_test.go`
- `internal/assurance/facetbreadth/core_test.go`
- `internal/assurance/facetbreadth/realtrace_shadow_test.go`

文档：

- `docs/semantic-assurance-rebuild/stage5/STAGE5_IMPLEMENTATION_REPORT.md`
- `docs/semantic-assurance-rebuild/stage5/COMMAND_RESULTS.md`

没有修改任何已有文件。

## 3. 公开 API

主要构建入口：

```go
func BuildCatalogIdentityV1([]facet.Evaluator) (CatalogIdentityV1, error)

func BuildCandidateSummaryV1(
    executionrecord.CompletedExecutionRecordV1,
    []facet.EvaluationV1,
) (CandidateFacetSummaryV1, error)

func NewCoverageStateV1(CatalogIdentityV1) (*CoverageStateV1, error)

func (*CoverageStateV1) Apply(
    ordinal uint64,
    summary CandidateFacetSummaryV1,
) (DecisionV1, error)

func (*CoverageStateV1) Snapshot() CoverageSnapshotV1
func (*CoverageStateV1) Digest() (string, error)
```

主要公开值类型包括：

- `CatalogFacetIdentityV1`
- `CatalogIdentityV1`
- `FacetKeySummaryV1`
- `FacetEvaluationSummaryV1`
- `CandidateFacetSummaryV1`
- `RepresentativeRefV1`
- `DecisionReasonV1`
- `DecisionV1`
- `CoverageKeySnapshotV1`
- `DecisionReasonCountV1`
- `CoverageSnapshotV1`

## 4. Schema 与确定性 digest

固定 schema：

| 对象 | schema ID | major |
|---|---|---:|
| Catalog | `modelfuzz-ng-facet-catalog-identity-v1` | 1 |
| Candidate summary | `modelfuzz-ng-candidate-facet-summary-v1` | 1 |
| Coverage state | `modelfuzz-ng-facet-breadth-state-v1` | 1 |

所有 digest 使用 typed payload：

```text
encoding/json.Marshal
-> SHA-256
-> 64 位小写十六进制
```

没有使用 `map[string]any` 作为 schema，也没有依赖 pointer、文件路径、wall-clock、
evaluator 注册顺序或 map iteration。

## 5. Catalog identity

Catalog 严格接受三个冻结 Facet：

| Facet | Scope | Classes |
|---|---|---:|
| `raft.election_role_term_shape` v1 | state | 13 |
| `raft.replication_alignment_shape` v1 | state | 8 |
| `raft.snapshot_lifecycle_event` v1 | transition | 10 |

每个 class-set digest 包含 `facet_id`、`facet_version`、`scope` 和排序后的完整
`class_ids`。Catalog fingerprint 只包含 schema/version 与排序后的 Facet
identity/class-set digest。

实现拒绝 nil、重复、缺失、额外、未知或 class/version/scope 被修改的 evaluator。
它不保留 evaluator pointer；Name、Rationale、class Description 等说明字段不影响
identity。返回 slice 均为 defensive copy。

## 6. Candidate summary

`CandidateFacetSummaryV1` 只保留：

- RecordDigest、Candidate ID、run index；
- PlanDigest、Plan action count；
- TraceDigest、Trace step count；
- Catalog fingerprint；
- 三项排序后的 evaluation status；
- evaluated Facet 的 typed key、canonical string、KeyDigest 和 first occurrence；
- typed summary payload 的 `SummaryDigest`。

明确不保存完整 Record、Plan、Trace、Observation、Model Event/State、Finding、
Explanation、Detail、Artifact 或 debug error。

Builder 校验 Record schema/version、digest、identity、非负 count；严格要求三项冻结
Facet evaluation。Key 会按 canonical string 排序去重；完全相同 key 的重复
observation 选择 Trace 顺序更早的 occurrence。typed key、canonical string、
digest、class、scope 或 occurrence 矛盾会返回 error。输入与输出均不共享可变 slice。

`CatalogFingerprint` 是为 Apply 的 Catalog mismatch 验证增加的紧凑 identity 字段；
`SummaryDigest` 是 Stage 5 规格明确允许的可选确定性摘要。

## 7. Eligibility

结构错误是 error；完整但 evidence 状态不满足规则则是成功的
`ineligible_evidence` decision。

Eligible 条件：

- 三项 evaluation identity 完整合法；
- Election 为 `evaluated`；
- Replication 为 `evaluated`；
- Snapshot 为 `evaluated` 或 `not_applicable`；
- 无 `invalid_evidence` / `insufficient_evidence`；
- 总 key 数至少 1；
- 所有 key 属于当前 Catalog 且 typed/canonical/digest 一致。

Engine/Experiment/TLC/Oracle outcome 不在 Candidate summary 中，因此不会被 Breadth
Core 重新分类。测试明确覆盖 runtime、mapping、oracle 和 model failure status 下的
合法 Trace prefix eligibility。

## 8. Apply ordinal、Decision 与原子性

`NextApplyOrdinal` 初始为 0。Apply 只接受严格相等的 ordinal。结构合法的 eligible
和 ineligible candidate 都推进：

- `AppliedCandidateCount`
- `EligibleCount` 或 `IneligibleCount`
- 对应 reason count
- `NextApplyOrdinal`

封闭 reason：

- `new_facet_class`
- `shorter_representative`
- `new_and_shorter`
- `no_novelty`
- `ineligible_evidence`

前三项 `Admitted=true`；这里的 admitted 只表示进入 Breadth representative set，
不表示进入现有 Corpus。

Apply 在锁内先完整验证 summary、ordinal、Catalog、key 和 representative identity，
再克隆 pre-state、计算全部 new keys/shortest replacement、验证完整 post-state，
最后一次替换 state。任何错误时 key、representative、counter、ordinal 和 digest
完全不变。一个 candidate 的多 key 更新不会部分提交。

## 9. First 与 Shortest

新 key 首次成功 Apply 时：

```text
First = current
Shortest = current
```

First 永不替换。Shortest 固定按以下五字段词典序比较：

1. Plan action count；
2. Trace step count；
3. PlanDigest；
4. TraceDigest；
5. RecordDigest。

Candidate ID、run index、apply ordinal、duration 和完成时间不参与比较。相同
RecordDigest 再出现时，其余语义 identity 字段必须一致；完全相同 ref 是 no-op。

## 10. Snapshot、StateDigest 与并发边界

`CoverageSnapshotV1` 使用排序 slice，不暴露内部 map。它包含 Catalog identity、
排序 covered keys、First/Shortest、三类 apply count、固定顺序五种 reason count、
NextApplyOrdinal 和 StateDigest。

StateDigest 包含：

- schema/version；
- Catalog fingerprint；
- 排序 key 与 typed First/Shortest ref；
- applied/eligible/ineligible count；
- 固定顺序 reason count；
- NextApplyOrdinal。

State 使用实例级 `sync.RWMutex`。Apply 由调用方串行拥有；Snapshot/Digest 可并发
只读。没有 goroutine、scheduler 或 package-level mutable state。

## 11. 紧凑上界

测试一次性 Apply 完整冻结 Catalog，得到：

- 31 covered keys；
- 62 个逻辑 First/Shortest slots。

10,000 次后续 no-novelty Apply 后：

- key 数和 representative slot 数不增长；
- State 不保存 candidate/Decision history；
- applied count 与 ordinal 正常增长。

## 12. 真实轨迹 shadow

`realtrace_shadow_test.go` 以 test-only 方式复用了 Stage 4 的同一组配置和公开资产：

- 真实 etcd-raft Adapter；
- 真实 Runtime、Engine、Mapper 和 Raft Oracle；
- `experiment.RunFeedback` 形成 Completion；
- `executionrecord.BuildV1`；
- `facet.EvaluateAll(..., raft.CatalogV1())`；
- `BuildCandidateSummaryV1`；
- 按固定场景顺序 Apply 0..7。

8 个场景均成功，最终 union：

| Facet | Keys |
|---|---:|
| Election | 6 |
| Replication | 4 |
| Snapshot | 10 |
| 合计 | 20 |

固定顺序下 8 个 decision 均为可解释的 `new_facet_class`；没有为了制造其他 reason
改变场景，五种 reason 由纯单元测试完整覆盖。两套独立 State 对相同序列产生完全
相同 Snapshot/Digest。每个 key 都有 First/Shortest。

Shadow 测试没有启动 TLC、没有 replay、没有 Artifact 写入、没有 mutation
candidate；Corpus outcome 由现有 Runner 产生且 Breadth 前后不变。

## 13. Feature-off 等价

使用两个独立、相同配置的真实 election 场景：

- off：执行到 Facet evaluation 后停止；
- on：随后构建 summary、State 并 Apply。

Engine Result、Completed Record、Facet evaluations、model executor call count 和
Experiment Run 的全部语义字段一致。`Run.DurationMillis/DurationMicros` 是两个独立
执行必然不同的 wall-clock debug 字段，比较前归零；它们不进入任何 Stage 5 identity、
summary 或 state。单次 on 路径还在 Breadth 操作前后对原始 Result、Run、Record 和
evaluations 做了精确不变性检查。

## 14. 测试结果与覆盖率

- package `-count=1`：通过；
- package `-count=20`：通过；
- package race：通过；
- statement coverage：`86.7%`；
- Stage 1 executionrecord：通过；
- Stage 3/4 facet（含 real-trace Pilot）回归：通过；
- 全仓 test：允许既有 httptest 本地回环后通过；
- 全仓 race：允许既有 httptest 本地回环后通过；
- 全仓 vet：通过。

受限 sandbox 中全仓 test/race 的首次失败仅为既有 `httptest` 无权监听
`[::1]:0`；相同代码状态允许本地回环后全部通过，未修改测试。

## 15. 范围确认

本阶段没有：

- 修改 Runner、OnRunComplete、CLI 或任何 Frozen Kernel；
- 接入 Corpus、mutation、energy、parent selection；
- 写 Artifact 或实现 writer/loader/checkpoint；
- 实现 active Facet mode 或 cross-Facet key；
- 实现 Goal、Waypoint、Frontier、Handoff、Assurance Matrix 或 Agent；
- 启动真实 TLC 或 replay；
- 修改 Stage 0—4 文件。

实现与 `FACET_BREADTH_V1_CONTRACT.md` 一致；没有触发停止条件。完成后未进入
Stage 6。
