# Stage 3 Offline Facet Core v1 实现报告

## 1. 结论

Stage 3 已在不修改 Frozen Kernel、Stage 0—2 文档或
`internal/assurance/executionrecord` 的前提下，实现只读、离线、确定性的 Facet
Core v1，以及 Stage 2 冻结的三个 Raft Facet：

- `raft.election_role_term_shape` v1：13 个 class；
- `raft.replication_alignment_shape` v1：8 个 class；
- `raft.snapshot_lifecycle_event` v1：10 个 class。

实现只消费调用方显式提供的 `CompletedExecutionRecordV1`、可选
`InitialObservation`、`Trace`、`ModelEvents` 和 `ModelStates`。它不读取 Artifact，
不读取 Runtime/RawNode，不调用 Mapper、TLC、Oracle 或 replay，不修改 Corpus，
也不持有 campaign 状态。

## 2. 实际新增文件

生产代码：

- `internal/assurance/facet/definition.go`
- `internal/assurance/facet/key.go`
- `internal/assurance/facet/evidence.go`
- `internal/assurance/facet/evaluate.go`
- `internal/assurance/facet/raft/catalog.go`
- `internal/assurance/facet/raft/semantic.go`
- `internal/assurance/facet/raft/election.go`
- `internal/assurance/facet/raft/replication.go`
- `internal/assurance/facet/raft/snapshot.go`

测试：

- `internal/assurance/facet/core_test.go`
- `internal/assurance/facet/evidence_test.go`
- `internal/assurance/facet/raft/evaluator_test.go`
- `internal/assurance/facet/raft/fixture_test.go`
- `internal/assurance/facet/raft/metamorphic_test.go`
- `internal/assurance/facet/raft/equivalence_test.go`

人工 Golden Fixture：

- `internal/assurance/facet/testdata/facet-v1/manifest.json`
- `internal/assurance/facet/testdata/facet-v1/election.json`
- `internal/assurance/facet/testdata/facet-v1/replication.json`
- `internal/assurance/facet/testdata/facet-v1/snapshot.json`

文档：

- `docs/semantic-assurance-rebuild/stage3/STAGE3_IMPLEMENTATION_REPORT.md`
- `docs/semantic-assurance-rebuild/stage3/COMMAND_RESULTS.md`

生产代码共 9 个文件、1,448 行；测试代码共 6 个文件、1,817 行。没有新增第三方依赖。

## 3. 依赖方向

实际直接依赖为：

```text
internal/assurance/facet
  -> internal/assurance/executionrecord
  -> internal/core
  -> internal/model

internal/assurance/facet/raft
  -> internal/assurance/facet
  -> internal/core
```

`facet` 对 `model` 的使用仅限可选 `model.Event`/`model.State` 的只读 count
一致性检查；不解析 `model.State.Text`，也不以 `model.State.Key` 分类。

生产代码没有直接导入 `runtime`、`engine`、`experiment`、`corpus`、adapter、
`model/raft`、`model/tlc`、`oracle`、`persistence`、`trace` 或 `minimize`。既有
package 没有反向 import `facet`。测试中的短时 Engine/Runtime fake 仅用于
feature-off 等价性。

## 4. 公开 API

Core 公开类型和入口：

- `Scope`：封闭值 `state`、`transition`；
- `Grounding`：`model_grounded`、`implementation_grounded`、`cross_layer`；
- `EvaluationStatus`：`evaluated`、`not_applicable`、
  `insufficient_evidence`、`invalid_evidence`；
- `EvidenceRequirement`：仅包含三个冻结 Facet 实际需要的证据类型；
- `ClassDefinition`、`InvarianceSet`、`DefinitionV1`；
- `KeyV1`、`NewKeyV1`、`CanonicalString`、`Digest`；
- `Occurrence` 及四个明确的构造函数；
- `ObservationV1`、`EvaluationV1`、`EvaluationInputV1`；
- `Evaluator`、`EvaluateAll`；
- `PrepareInputV1`、`NewObservation`、`NewEvaluation`。

Raft Catalog 公开入口：

- `NewElectionRoleTermShapeV1() facet.Evaluator`
- `NewReplicationAlignmentShapeV1() facet.Evaluator`
- `NewSnapshotLifecycleEventV1() facet.Evaluator`
- `CatalogV1() []facet.Evaluator`

`CatalogV1` 每次返回新的 evaluator 实例，顺序固定为 Election、Replication、
Snapshot；没有全局 mutable registry 或 `init` 注册。

## 5. Definition validation 与不可变边界

`DefinitionV1.Validate` 检查：

- namespaced、安全字符的 `facet_id`；
- 正版本、非空 name/protocol/rationale/validation status；
- 封闭 scope/grounding；
- 非空、严格排序、无重复且属于有限枚举的 evidence requirement；
- 严格排序且无重复的 property family；
- 非空、严格排序且无重复的 class；
- `CardinalityBound >= len(Classes)`。

三个 Catalog 定义进一步由测试冻结为 `CardinalityBound == class 数`。
`Definition()` 和 `DefinitionV1.Copy()` 复制所有 slice，调用方修改返回值不会污染
evaluator。

## 6. FacetKey v1

固定 schema：

```text
modelfuzz-ng-facet-key-v1
```

canonical string：

```text
modelfuzz-ng-facet-key-v1/<facet_id>/v<version>/<scope>/<class_id>
```

digest 对以下 typed JSON 按字段声明顺序执行
`encoding/json.Marshal -> SHA-256 -> 64 位小写十六进制`：

1. `schema_id`
2. `facet_id`
3. `facet_version`
4. `scope`
5. `class_id`

NodeID、MessageID、ExecutionID、seed、run/step/effect index、绝对 term/index、
Artifact、RecordDigest 和 explanation 均不进入 Key 或 digest。每个 Facet 都有
至少一个静态、非运行时生成的 expected digest；实际 Golden 测试对 31 个 class
全部执行精确比对。

## 7. Occurrence、候选并集和原子评价

`Occurrence` 封闭为：

- `explicit_initial_state`
- `trace_initial_before`
- `trace_step_after`
- `transition_effect`

不适用的 index 使用 `nil`，不存在把默认 0 误判为“未提供”的情况。

单 evaluator 的 observation 先按 occurrence 顺序产生，再以 Key canonical string
去重，保留第一次 occurrence，最后按 canonical string 排序。Occurrence 和
Explanation 不进入 Key。

正常 evidence 状态通过 `EvaluationStatus` 返回且 `error == nil`。只有 definition、
重复/nil evaluator、未知 class 或其他实现不变量错误才使用 error。任一 required
occurrence 缺字段或非法时，整个 evaluator 返回
`insufficient_evidence`/`invalid_evidence`，Observations 为空，不保留部分 key。

## 8. 公共 evidence 校验

`PrepareInputV1` 负责协议无关检查：

- Record schema/version；
- RecordDigest 为完整小写 SHA-256；
- Record 内 trace/model summary count 自洽；
- `InitialObservation.Validate()`；
- `Trace.Validate()`；
- Trace version、ExecutionID、seed、step count 与 Record 一致；
- Effect 总数与 Record 一致；
- 显式提供的 ModelEvents/ModelStates count 与 Record 一致；
- 显式提供的 ModelEvents 均通过 `model.Event.Validate()`。

公共层不检查 Raft semantic 字段，不重算 executionrecord digest，不比较 Artifact
文件摘要，不运行 replay。输入的 Initial 和 Trace 被复制为本次评价独占快照；
Record 只读取标量事实；ModelEvents/States 只遍历验证/count，不被保存。

## 9. nil/空 Trace 语义

- Record step count > 0 且 Trace 为 nil：三个 Facet 都是
  `insufficient_evidence`；
- Record step count == 0、Trace 为 nil：state Facet 有合法 Initial 时可评价，
  否则 insufficient；Snapshot insufficient；
- 合法空 Trace：state Facet 有 Initial 时评价 Initial，否则 insufficient；
  Snapshot `not_applicable`；
- 非空 Trace、无 Initial：state Facet 使用第一个 `NodesBefore`；
- 非空 Trace、有 Initial：按当前 evaluator 的窄 projection 检查一致，只评价一次
  explicit Initial；
- 每个 `NodesAfter` 都是 state occurrence；
- failure record 不会产生伪 transition。

相邻 Step 连续性只比较当前 Facet 需要的窄 projection，不比较无关 Semantic map。
`model_failed`、`oracle_failed` 和其他 partial completion 只要具有合法 Trace prefix
即可离线评价。

## 10. Election：13-class

输入为所有 `NodeObservation` 的 Status、`Semantic["role"]` 和
`Semantic["term"]`。running 节点 role 只能是 follower/candidate/leader 且 term
必须存在；crashed 节点 role 必须是 crashed，term 可省略但若存在必须合法。crashed
term 不参与 running term shape。

class 恰好为：

```text
leaders_none_candidates_none_terms_uniform
leaders_none_candidates_none_terms_split
leaders_none_candidates_some_terms_uniform
leaders_none_candidates_some_terms_split
leaders_one_candidates_none_terms_uniform
leaders_one_candidates_none_terms_split
leaders_one_candidates_some_terms_uniform
leaders_one_candidates_some_terms_split
leaders_multiple_candidates_none_terms_uniform
leaders_multiple_candidates_none_terms_split
leaders_multiple_candidates_some_terms_uniform
leaders_multiple_candidates_some_terms_split
no_running_nodes
```

空 nodes 为 invalid；required field 缺失为 insufficient；非法数值、role/status
矛盾为 invalid。multiple leaders 只产生语义 class，不被重解释为 Oracle finding。

## 11. Replication：8-class

对全部 observed node（包括 crashed）读取 `last_index`、`commit`、`applied`，先验证
`applied <= commit <= last_index`，再独立计算 log/commit/applied 的
aligned/diverged：

```text
log_aligned_commit_aligned_applied_aligned
log_aligned_commit_aligned_applied_diverged
log_aligned_commit_diverged_applied_aligned
log_aligned_commit_diverged_applied_diverged
log_diverged_commit_aligned_applied_aligned
log_diverged_commit_aligned_applied_diverged
log_diverged_commit_diverged_applied_aligned
log_diverged_commit_diverged_applied_diverged
```

单节点三个分量均 aligned；绝对 index 和 lag 不进入输出。

## 12. Snapshot：10-class

只遍历 `StepRecord.Effects` 中
`Effect.Kind == core.EffectModelEvent && Effect.ModelEvent != nil` 的 marker。

| 实际 marker | class | 必需字段/关系 |
|---|---|---|
| `raft.snapshot_created` | `snapshot_created` | valid Node；`index>0`；合法 `term`、`snapshot_bytes` |
| `raft.log_compacted` | `log_compacted` | 合法 boundary；`compact_index>0`；`compacted_entries>0` |
| `raft.snapshot_sent` | `snapshot_sent` | 合法 boundary；valid `to != Node`；`pending_snapshot==index`；`next_index==index+1` 且防溢出；`progress_state=="StateSnapshot"`；合法 `match_index` |
| `raft.snapshot_delivered` | `snapshot_delivered` | valid Node；合法 boundary |
| `raft.snapshot_applied` | `snapshot_applied` | valid Node；合法 boundary |
| `raft.snapshot_fast_forwarded` | `snapshot_fast_forwarded` | 合法 boundary；commit before/after 同时缺失或同时合法且 after >= before |
| `raft.snapshot_rejected_or_stale` | `snapshot_rejected_or_stale` | 合法 boundary；可选字符串 reason 只进入 explanation |
| `raft.snapshot_status_reported` | `snapshot_status_ignored` | valid from/to/reporter，from != to，to == reporter，`handled=false` 优先 |
| 同上 | `snapshot_status_failed` | `handled=true && reject=true` |
| 同上 | `snapshot_status_succeeded` | `handled=true && reject=false` |

未知 marker 和普通 Effect 被忽略；整个候选无 recognized marker 时
`not_applicable`。任何已识别 marker 非法都会原子地使整个 Snapshot evaluator 返回
`invalid_evidence` 且无部分 observations。

## 13. 窄动态类型 reader

非负整数 reader 接受：

- Go 全部无符号整数；
- 非负的 Go 有符号整数；
- 能被 `strconv.ParseUint(..., 10, 64)` 精确接受的 `json.Number`；
- finite、非负、无小数且严格小于 `2^64` 的 float32/float64。

它拒绝负数、分数、NaN、Inf、`>=2^64`、bool、数字字符串及未知动态类型。bool 和
string reader 只接受精确的 Go `bool`/`string`。没有 reflection 式通用 schema。

## 14. Fixtures 与 manifest

Fixture 是 test-only typed schema，不是生产 DSL：

- `election.json`：13 cases，5,735 bytes，
  SHA-256 `9578912963c7fb6a65744fada39be41b320fe73ff83ac89cd706de298e1fb66e`；
- `replication.json`：8 cases，3,224 bytes，
  SHA-256 `755270aeb9c76054c538e15a568cbfc763b060b815dbb582753c5f039a134aff`；
- `snapshot.json`：10 cases，4,497 bytes，
  SHA-256 `3676bfb8d0087a34ce824395bb6afe4bbee32941ebdd82986399cf7df9829b84`。

总计 31 cases、13,456 fixture bytes。测试严格读取 manifest，验证每个文件的
实际 bytes/SHA、fixture schema、case 总数、唯一 fixture ID、静态 expected
canonical key、静态 expected digest 和 expected first occurrence，并证明每个冻结
class 恰好至少被覆盖一次。没有 update-golden 开关。

## 15. Metamorphic 与 feature-off 结果

通过的变换包括：

- node rename 与 node slice 重排；
- MessageID 及 action/marker endpoint rename；
- uniform positive term shift；
- uniform positive log/snapshot index shift；
- Artifact 路径和文件摘要变化；
- ExecutionID/seed/RecordDigest/debug text 变化；
- Semantic/marker map 插入顺序变化；
- 相同 `model.State.Key`、不同 `Text`；
- 同一输入重复至少 20 次。

所有变换保持 Facet key 集不变。

feature-off 测试运行两个独立、同配置的短时 fake
Runtime/Engine/Mapper/model-executor：

1. A 仅执行固定 Timeout Plan；
2. B 执行同一 Plan，结束后调用 `BuildV1` 和 `CatalogV1`。

两次 `engine.Result` 深比较完全一致，覆盖 status/error、resolution、Actions、
Effects、Trace、ModelEvents、ModelStates、OracleFindings、Failure、Initial/Final；
两侧 model executor 调用次数都为 1。Facet 只在执行完成后调用，没有启动 TLC、
fuzz campaign、Corpus、mutation 或 Artifact writer。

## 16. 测试、覆盖率和依赖审计

- package test：通过；
- `-count=20`：通过；
- package race：通过；
- executionrecord 回归：通过；
- 全仓普通测试：允许本地 loopback listener 后通过；
- 全仓 race：允许本地 loopback listener 后通过；
- `go vet ./...`：通过；
- `gofmt -l internal/assurance/facet`：无输出；
- Core 覆盖率：80.7% statements；
- Raft 覆盖率：91.4% statements；
- 禁止生产 import 的 `rg`：无匹配。

受限沙箱中的全仓普通测试会因既有 `httptest.NewServer` 无权监听 `[::1]:0` 而失败；
相同代码在允许本地回环监听的环境中通过。这是沙箱环境限制，不是 Stage 3 或基线
代码失败。命令和首个错误见 `COMMAND_RESULTS.md`。

## 17. 与 Stage 2 规格的偏差和限制

没有 Facet 语义偏差。实现使用 Stage 2 冻结的三个 Facet、31 个 class、证据边界、
Trace nil/empty 语义、不变量和 snapshot marker schema。

唯一实现层选择是 fixture 将相同文件中共享的 expected occurrence 提升为文件级
typed 字段，case 仍静态保存 expected class/key/digest；这不改变语义，也不成为生产
schema。

当前限制与 Stage 2 一致：

- 不提供 Artifact loader，因此 offline caller 必须显式提供 typed evidence；
- 不验证 RecordDigest 内容，只验证公开格式，避免复制 executionrecord 私有算法；
- 不解析 model state 文本；
- 不计算 Facet novelty、交叉 Facet key、Goal progress 或 Corpus admission。

## 18. Stop condition 与范围确认

没有触发 Stage 3 stop condition：实际 marker、Effect schema、node semantic 与冻结
Catalog 可表达且一致；无需修改 Frozen Kernel、executionrecord 或 Stage 2 文档；
没有 package cycle，也不需要 Runtime/RawNode、TLC、replay、Artifact loader、
registry、DSL、plugin 或 Goal history。

明确确认本阶段没有实现 Facet novelty、Facet admission、Facet energy、Goal、
Waypoint、Frontier、Handoff、Assurance Matrix、Agent 或 proposal schema，也没有
进入 Stage 4。
