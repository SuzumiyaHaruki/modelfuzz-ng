# Facet v1 语义契约

## 1. 状态与权威范围

本文是 Stage 3 Offline Facet Core v1 的语义权威。本文只冻结定义，不实现
evaluator、coverage、Corpus admission、Goal 或 Agent。

审计基线：

- branch：`agent/semantic-assurance-rebuild-v1`
- HEAD：`75d4e51120b370acb880d003629f916da3f1a080`
- Completed Execution Record：
  `internal/assurance/executionrecord.CompletedExecutionRecordV1`
- Trace schema：`core.CurrentTraceVersion == 1`

## 2. Stage 1 边界复核

Stage 1 的实际代码与报告不存在会阻断本文的实质矛盾。

1. `internal/assurance/executionrecord` 是叶层 package。仓库内除其自身外没有
   package import 它；它只读取 `core`、`engine`、`experiment`、`minimize`
   和 `oracle` 类型。
2. `CompletedExecutionRecordV1` 只保存
   `CandidateIdentity`、`PlanSummary`、outcome summaries 和
   `ArtifactReference`；不内嵌 `plan.PlanSequence`、`core.Trace`、
   `core.Observation`、`[]model.Event`、`[]model.State` 或
   `[]oracle.Finding`。
3. `ArtifactReference.Validate` 只校验 kind、逻辑相对路径和完整 SHA-256；
   `BuildV1`、`DecodeV1` 均不读取文件。
4. 缺少 trace/model/finding artifact、缺少 failure signature，以及
   `Replayable=false` 都可以被显式表达。
5. `Replayable` 只表示 config 与 trace 引用以及 Trace identity 足以尝试
   replay；它不表示某个 Facet 所需的节点快照或 Effect 一定已加载。
6. `recordDigestPayload` 排除 artifact path/file SHA、`Replayable` 和 debug
   text；Facet 不得把 artifact 布局误当作执行语义。
7. `EngineOutcome` 与 `ExperimentOutcome` 是两个字段，允许
   `Result.Status` 与投影后的 `Run.Status` 不同。
8. `BuildV1` 和 `DecodeV1` 不访问 Runtime、Adapter、RawNode、TLC 或 Corpus。
9. `TestFeatureOffExecutionEquivalence` 验证 post-completion `BuildV1`
   不改变 Engine 结果或 model executor 调用次数。

因此 Facet v1 必须采用“Record 作为身份和证据索引、调用方显式提供 typed
evidence”的结构。`Record.Replay.Replayable` 不能代替逐 Facet evidence
validation。

## 3. 核心定义

Facet 是对一次已完成执行中某个具体状态或具体转换的、确定性的、有限基数的
协议语义投影。它回答：

> 该状态或转换在某一个独立语义维度上属于哪一个类别？

Facet v1 不是：

- 一个全局统一的 `CanonicalState`；
- 完整模型状态或 TLC fingerprint 的替代品；
- Goal、Waypoint、temporal objective 或 sticky history；
- reward、Corpus 或 mutation policy；
- Oracle finding 或 model-conformance verdict；
- Agent 的自由文本判断；
- 实现指标的任意 bucket 集合。

一个状态可以同时在多个独立 Facet 中各产生一个 class。Facet v1 不生成多个
Facet 的 Cartesian product，也不生成 pairwise combination key。

## 4. 与现有概念的边界

| 概念 | 当前实现 | 与 Facet v1 的关系 |
|---|---|---|
| raw TLC state coverage | `model.State.Key`，由 `experiment.defaultCoverageProjection`/Corpus 维护 | 原后端 opaque identity；Facet 不替代它 |
| semantic state coverage | `model/raft.ProjectCoverage` 从 `model.State.Text` 生成 `int64` key | 现有 whole-state 投影；Facet 是多个独立、有限、可解释维度 |
| semantic transition coverage | `ProjectCoverage` 对 projected-before、`Event.Name`、projected-after hash | 现有 whole-transition key；Facet 不替代它 |
| Goal progress | 当前 Stage 2 不存在 | Goal 可以读取 Facet observation，但不能改变 Facet class |
| Oracle result | `[]oracle.Finding` | Facet 不宣告安全/失败，也不重新解释 finding |
| Model conformance | Mapper + strict controlled TLC outcome | Facet 不调用 TLC，不把 classification 当作 conformance |
| implementation metrics | `metrics.RunMetrics` | 可作为审计或对照，不自动成为 Facet key |

`internal/model/raft/coverage.go` 的 `ProjectCoverage` 确实包含经过
`coverage_test.go` 测试的 TLA state text projector，但其结构化解析函数
`projectState`、`stateAssignments` 等是 package-private，公开结果只有 hashed
coverage keys。Stage 3 不解析 `model.State.Text`，不复制正则 parser，也不把
`model.State.Key` 当作 Facet 语义。未来若要 model-grounded Facet，需要一个另行
冻结的 typed model projection 边界，而不是正则解析 TLC 展示文本。

## 5. 唯一支持的 Scope

### 5.1 `state`

对一个节点状态快照做纯函数分类。合法来源是：

- `engine.Result.Initial.Nodes`；
- 非空 Trace 的首个 `StepRecord.NodesBefore`；
- 每个 `StepRecord.NodesAfter`。

Stage 3 的调用方可显式提供 `InitialObservation`。Trace 本身不保存每一步完整
Observation，只保存节点快照。

### 5.2 `transition`

对一条已执行转换做纯函数分类。合法来源是一个完整
`core.StepRecord`：

- `NodesBefore`
- `Action`
- `Effects`
- `NodesAfter`

如某定义明确需要对应 `model.Event`，调用方还必须提供无歧义的绑定。当前
`engine.Result.ModelEvents` 是扁平 slice，未保存 event-to-step 边界，因此 v1
Catalog 的三个 Facet均不依赖这种绑定。

### 5.3 明确不支持

- arbitrary execution-history Facet；
- 多步 temporal pattern；
- “曾发生 X 后又发生 Y”；
- prefix progress、sticky evidence、waypoint sequence；
- 跨不定数量 Step 的状态机。

“一次 transition 记录了 snapshot send”是合法 transition Facet observation。
“第一次 snapshot 发送失败后，heartbeat 触发重试并最终安装”是 Goal/Waypoint，
不是 Facet v1。

## 6. Evaluation 规则

1. evaluator 只能读取调用方提供的 immutable evidence view。
2. evaluator 不得读取 Runtime/RawNode、调用 Adapter、启动 TLC、重放 Trace、
   修改 Record/Corpus 或写 Artifact。
3. candidate coverage 是对合法 initial state、每个合法 `NodesAfter` state 和
   每个合法 transition 求值所得 `FacetKey` 的集合并集。
4. 同一 key 在同一 candidate 重复出现只计一次；可记录按 trace 顺序的第一次
   occurrence。
5. occurrence 的 step index、effect index 和 explanation 不进入 key identity。
6. `runtime_failed`、`mapping_failed`、`model_failed`、`oracle_failed` 等 status
   不自动禁止 evaluation。只要 Trace 中存在通过 `StepRecord.Validate` 的完整前缀，
   就评价该前缀。
7. 失败 Action 不在 `Trace.Steps`；`core.FailureRecord.ObservationBefore` 只能作
   debug/failure evidence，不能伪装成一个完整 transition。
8. evaluator 不重分类 Engine/Experiment status，不修改 failure signature。
9. evaluator 输入或返回值不得暴露会被调用方修改后污染内部结果的 map/slice。
10. 遍历顺序固定为：initial state（若有）、Trace step index 升序；每步先 state
    observation，再按 Effect 原 slice index 处理 transition evidence；最终按
    `FacetKey` canonical string 排序返回 candidate union。

## 7. EvaluationStatus

封闭枚举：

| status | 含义 |
|---|---|
| `evaluated` | 所需 evidence 完整且校验成功，产生零个或多个合法 observation |
| `not_applicable` | evidence 合法，但当前 state/transition 不在 Facet 定义域 |
| `insufficient_evidence` | 定义所需 evidence 未保留或未由调用方提供 |
| `invalid_evidence` | evidence 与 Record identity/count/version 矛盾，或必需字段类型/关系非法 |

规则：

- 对 state Facet，合法 state 通常产生一个 class。
- 对 snapshot transition Facet，一条无 snapshot marker 的合法 transition 是
  `not_applicable`；一条 transition 可含多个不同 lifecycle marker，因此可产生
  多个 observation。
- 整个 candidate 缺 Trace 时，依赖 Trace 的 evaluator 返回
  `insufficient_evidence`，不是空 coverage。
- 普通缺失不得 panic，也不得包装为 Engine failure。
- evaluator 实现 bug 可在 Stage 3 的 Go API 中返回 `error`，但 `error` 不是第五种
  正常 evaluation status。

## 8. FacetDefinition v1 元数据

每个冻结 definition 必须声明：

- `facet_id`
- `facet_version`
- human-readable name
- `protocol`
- `grounding`
- `scope`
- rationale
- related property families
- required evidence
- optional evidence
- exact class catalog
- class membership rules
- invariances
- cardinality bound
- not-applicable rules
- missing-evidence behavior
- source bindings
- positive examples、boundary examples、counterexamples
- validation status

`grounding` 是封闭枚举：

- `model_grounded`
- `implementation_grounded`
- `cross_layer`

未来 Agent 可以提案 rationale、evidence binding、finite class catalog、exact
predicates、invariances、examples 和 cardinality bound；不能在运行时自由判断
class、读取 RawNode、动态改定义、把自然语言放进 key、绕过 validator、修改
Corpus/TLA+/Mapper。接受的 definition 必须在 campaign 开始前冻结版本，并由确定性
evaluator 执行。

## 9. FacetKey v1

canonical typed payload 固定为：

```text
schema_id
facet_id
facet_version
scope
class_id
```

其中：

- `schema_id = modelfuzz-ng-facet-key-v1`
- `facet_id` 使用 namespace，例如 `raft.election_role_term_shape`
- `facet_version` 是正整数
- `scope` 只能是 `state` 或 `transition`
- `class_id` 必须属于该版本 definition 的封闭 catalog

建议 Stage 3 同时提供：

- canonical string：
  `modelfuzz-ng-facet-key-v1/<facet_id>/v<version>/<scope>/<class_id>`
- 对 typed payload 经 `encoding/json.Marshal` 后的完整小写 SHA-256

FacetKey 禁止包含：

- raw NodeID、MessageID、ExecutionID；
- run index、seed、step/effect index；
- absolute term、absolute log/snapshot index；
- Artifact path、wall-clock time；
- debug explanation、任意 map、任意动态字符串或完整 state JSON。

关系只能使用 definition 中冻结的有限 enum，例如 `terms_uniform`、
`terms_split`。Observation metadata 可以包含首次 occurrence 和解释，但它们不是
identity。

## 10. 强制不变量

每个 Catalog Facet 必须逐项声明：

1. Node renaming invariance
2. MessageID renaming invariance
3. uniform term shift invariance
4. uniform log/index shift invariance
5. Artifact layout invariance
6. ExecutionID/seed invariance
7. map iteration invariance
8. unrelated debug text invariance

“先按 raw NodeID 排序再把 ID 放进 key”不构成 node-renaming canonicalization。
v1 三个 Facet只使用数量、相等关系或事件类别；raw identity 不进入 predicate 的
输出。

## 11. 基数与组合

- 每个 Facet 必须有理论最大 class 数。
- 禁止每 term/index/MessageID/node permutation 一个 class。
- 禁止无上限 lag、任意字符串或完整状态 JSON。
- 若使用 bucket，边界必须在 definition 中明确。
- 每个 Facet独立产出 key；v1 不产出跨 Facet combination key。

## 12. Offline-only 冻结

Stage 3 仅实现 offline evaluation：

- 不替代 raw/semantic coverage；
- 不修改 Corpus admission 或 checkpoint；
- 不参与 mutation、Goal 或 Plan 解析；
- 不接入 Runner/CLI；
- feature off 时 baseline 的执行、TLC 调用、Oracle、artifact 和 CLI 语义完全
  不变。

Facet novelty 与 Corpus 的关系在本阶段未定义。任何“新 Facet 自动 admission”
都超出 v1 语义契约。
