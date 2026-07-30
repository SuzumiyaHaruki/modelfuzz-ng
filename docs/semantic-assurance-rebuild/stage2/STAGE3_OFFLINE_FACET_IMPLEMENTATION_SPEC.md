# Stage 3 Offline Facet Core v1 实现规格

## 1. 唯一目标

Stage 3 只实现：

> 一个只读、离线、确定性的 Facet Core v1，以及
> `RAFT_FACET_CATALOG_V1.md` 冻结的三个 Raft Facet。

Stage 3 不接入 Runner/CLI/Corpus，不持久化 Completed Record，不实现 artifact
loader、Facet novelty/admission、Goal、Agent、DSL、registry、plugin 或 provider。

## 2. Package 与依赖

推荐：

```text
internal/assurance/facet/
  definition.go
  key.go
  evidence.go
  evaluate.go
  *_test.go
  testdata/facet-v1/*.json

internal/assurance/facet/raft/
  catalog.go
  semantic.go
  election.go
  replication.go
  snapshot.go
  *_test.go
```

允许根据规模合并文件，不得增加全局 registry。

依赖方向：

```text
internal/assurance/facet
  -> internal/assurance/executionrecord
  -> internal/core, internal/model（只读类型）

internal/assurance/facet/raft
  -> internal/assurance/facet
  -> internal/core
```

已有 package 不得 import `facet`。特别禁止修改
`internal/engine`、`runtime`、`experiment`、`model`、`oracle`、`corpus`、
`cmd/modelfuzz-ng`。

## 3. 推荐公开类型

名称可按 Go 风格微调，职责不得扩大。

```go
type Scope string // state | transition
type Grounding string
type EvaluationStatus string

type DefinitionV1 struct {
    ID                 string
    Version            uint32
    Name               string
    Protocol           string
    Grounding          Grounding
    Scope              Scope
    Classes            []ClassDefinition
    RequiredEvidence   []EvidenceRequirement
    OptionalEvidence   []EvidenceRequirement
    CardinalityBound   int
    Invariances        InvarianceSet
}

type KeyV1 struct {
    SchemaID    string
    FacetID     string
    FacetVersion uint32
    Scope       Scope
    ClassID     string
}

type ObservationV1 struct {
    Key          KeyV1
    KeyDigest    string
    Occurrence   Occurrence
    Explanation  string
}

type EvaluationV1 struct {
    FacetID      string
    FacetVersion uint32
    Status       EvaluationStatus
    Observations []ObservationV1
    Detail       string
}
```

`Detail`/`Explanation` 不参与 key 或 digest。

Evaluator 是最窄只读接口：

```go
type Evaluator interface {
    Definition() DefinitionV1
    Evaluate(input EvaluationInputV1) (EvaluationV1, error)
}
```

调用方显式构造：

```go
evaluators := []facet.Evaluator{
    raft.NewElectionRoleTermShapeV1(),
    raft.NewReplicationAlignmentShapeV1(),
    raft.NewSnapshotLifecycleEventV1(),
}
```

禁止 `init` 注册、mutable singleton、字符串查找 registry 或 callback list。
`raft.CatalogV1()` 可以返回上述固定 slice 的全新副本，但不得持有可变全局状态。

## 4. EvaluationInput 与 ownership

推荐：

```go
type EvaluationInputV1 struct {
    Record             executionrecord.CompletedExecutionRecordV1
    InitialObservation *core.Observation
    Trace              *core.Trace
    ModelEvents        []model.Event
    ModelStates        []model.State
}
```

规则：

- `Record` 是必需身份/summary。
- Initial/Trace/Events/States 由调用方显式提供；package 不从
  `ArtifactReference.Path` 读取文件。
- 三个 Catalog evaluator 只需要 Initial/Trace。Events/States 为未来
  model-grounded evaluator 和 consistency checks 保留，不要求本阶段解析 Text。
- constructor 或 `Evaluate` 必须 defensive-copy 可变 slice/map，或在入口复制为
  package 内 immutable typed projection；不得持有调用方指针。
- 不在 Record 或 Trace 中缓存评价结果。

## 5. Evidence consistency validation

在任何 Facet predicate 前执行公共 validation：

1. Record schema/major version 正确，`RecordDigest` 格式合法；不得复制
   executionrecord 的私有 digest 算法来“重验”。
2. Trace 缺失时，依赖 Trace 的 evaluator 返回 `insufficient_evidence`。
3. Trace 存在时：
   - `Trace.Validate()` 成功；
   - version、ExecutionID、seed、step count 与 `Record.Trace` 一致；
   - `Record.Engine.TraceStepCount == len(Trace.Steps)`；
   - Effect 总数与 `Record.Engine.EffectCount` 一致。
4. 提供 ModelEvents/States 时，长度分别等于 Record counts；不提供时不得因本
   Catalog 不需要它们而失败。
5. 提供 Initial 时 `Initial.Validate()` 成功。Trace 非空时，它的 Nodes 与首个
   `NodesBefore` 在 Facet 所需字段上必须一致。
6. 相邻 steps 的 `previous.NodesAfter` 与 `next.NodesBefore` 在 ID/Epoch/Status
   及 Catalog 所需 semantic fields 上一致。
7. 不重新实现 `experiment.digestTrace`、`digestStatePath`，不要求 artifact file
   SHA 等于 semantic digest。
8. count/version/identity 矛盾返回 `invalid_evidence`；普通缺失返回
   `insufficient_evidence`。

校验不得调用 Runtime、Mapper、TLC、Oracle 或 replay。

## 6. FacetDefinition validation

Stage 3 Core 必须验证静态 definition：

- ID 有 namespace 且只含冻结的安全字符；
- version > 0；
- scope/grounding 是已知 enum；
- class ID 非空、唯一、固定顺序；
- cardinality bound > 0 且不小于 class 数；
- v1 Catalog 的 exact class set 与 Stage 2 文档一致；
- requirement/invariance 集合 canonical sorted；
- 返回 defensive copy。

不实现运行时加载任意 YAML/JSON definition；三个 definition 在 Go 中显式构造。

## 7. FacetKey 与 digest

固定：

```text
schema_id = modelfuzz-ng-facet-key-v1
```

typed digest payload 字段顺序：

```text
schema_id, facet_id, facet_version, scope, class_id
```

`encoding/json.Marshal` → SHA-256 → 64 位小写十六进制。Readable canonical
string 与 `FACET_V1_SEMANTIC_CONTRACT.md` 一致。

Core 必须拒绝：

- unknown facet/version/class；
- class 不属于 evaluator definition；
- scope 与 definition 不同；
- dynamic map/string 被拼进 class；
- 非 canonical key/digest。

Occurrence、explanation、RecordDigest、NodeID、step index 不参与 Key digest。

## 8. Candidate-level evaluation

一个 evaluator 对单 candidate 的算法：

1. validation 与 evidence availability 判定；
2. 按固定 occurrence 顺序调用 state/transition predicate；
3. 收集 observations；
4. 以 canonical key string 去重；
5. 保存每个 key 的第一次 occurrence；
6. 返回 observations 按 canonical key string 排序；
7. status：
   - 有至少一个合法 occurrence 被评价：`evaluated`；
   - evidence 完整但所有 transition 均不在定义域：`not_applicable`；
   - 所需输入缺失：`insufficient_evidence`；
   - 矛盾/非法：`invalid_evidence`。

state candidate 若有至少一个合法 state 必须产生一个或多个 observation。
snapshot candidate 可以完全 `not_applicable`。

若需要同时评价多个 evaluators，提供纯函数 helper：

```go
func EvaluateAll(input EvaluationInputV1, evaluators []Evaluator) ([]EvaluationV1, error)
```

helper 只按调用方 slice 顺序执行，验证 evaluator ID/version 唯一，最终结果按
facet_id/version 排序；不保存全局 union，不做 campaign novelty。

## 9. Raft evaluator 文件职责

- `raft/semantic.go`：窄的 typed reader，把 `NodeObservation.Semantic` 所需字段
  转为 string/uint64/bool；不提供任意 schema framework。
- `raft/election.go`：
  `raft.election_role_term_shape` 的 13-class predicate。
- `raft/replication.go`：
  `raft.replication_alignment_shape` 的 8-class predicate。
- `raft/snapshot.go`：
  recognized lifecycle marker schema 与 10-class predicate。
- `raft/catalog.go`：返回三个静态 evaluator 的新 slice。

不得 import `internal/adapters/etcdraft` 来读取内部 node/RawNode。source binding
只针对 `core` typed evidence 和冻结字段名。

## 10. Golden fixtures

Stage 3 创建小型、人工可审计 fixture，建议 schema
`modelfuzz-ng-facet-fixture-v1`。fixture 仅包含：

- Record 的最小合法 summary；
- typed Initial/Trace evidence；
- expected per-facet status；
- expected canonical keys/digests；
- expected first occurrences。

不得：

- 运行测试时覆盖 golden；
- 从当前 evaluator 反向生成 expected key；
- 内嵌大 snapshot/message payload；
- 使用真实历史 campaign 作为唯一 fixture。

每个 class 至少一个 fixture；可以让一个 fixture 覆盖多个 class。manifest 记录
文件 bytes 和 SHA-256，测试实际加载并执行全部 fixture。

## 11. 必须测试的职责

### 11.1 Core

1. `FacetKey` canonical string/digest deterministic。
2. unknown facet/version/class/scope rejection。
3. state scope evaluation。
4. transition scope evaluation。
5. candidate union dedup。
6. first occurrence 不进入 key。
7. input/result defensive copy。
8. evaluator 明确 slice，无 registry/global state。
9. 相同输入重复至少 20 次输出一致。

### 11.2 Invariance/metamorphic

10. node renaming：同时置换 NodeID、Action endpoint 和 marker endpoint 后 keys
    不变。
11. uniform term shift：所有 term 增同一常量后 keys 不变。
12. uniform log/index shift：last/commit/applied 与 snapshot boundary 同步平移后
    keys 不变。
13. MessageID renaming 后 keys 不变。
14. artifact path/SHA、ExecutionID/seed、debug text 改变不影响 keys。
15. Semantic map 插入顺序改变不影响输出。

### 11.3 Evidence

16. missing required evidence → `insufficient_evidence`。
17. Record/Trace count、version、seed、ExecutionID mismatch →
    `invalid_evidence`。
18. empty Trace：
    - 有 Initial 时 state Facet可评价；
    - 无 Initial 时 state insufficient；
    - 已提供合法 empty Trace 时 snapshot transition 为 `not_applicable`。
19. partial Trace 正常评价合法 prefix。
20. `model_failed` Trace prefix 正常评价。
21. `oracle_failed` Trace prefix 正常评价。
22. failure Action 不产生伪 transition。
23. evaluator 前后 Record/Trace 深比较不变。
24. fake Runtime/TLC/Corpus 调用计数保持 0；生产 package 不依赖这些 package。
25. 不解析 `model.State.Text`：改变 Text 不改变三个 Catalog 结果。

### 11.4 Catalog 完整性

26. election 13 个 class 全覆盖，role/status/type invalid cases。
27. replication 8 个 class 全覆盖，`applied<=commit<=last` validation。
28. snapshot 10 个 class 全覆盖。
29. snapshot status ignored 优先于 reject。
30. 单 transition 多 marker 输出多个 key，一种 marker 重复只留一个 candidate
    key。
31. known marker 缺 param → invalid；unknown marker → not applicable。
32. 三个 definition 的 theoretical class count 与 Catalog 一致。

### 11.5 Feature-off equivalence

复用 Stage 1 风格的两个独立短时 fake Engine：

- A：执行结束，不调用 Facet；
- B：执行结束后 BuildV1，再调用 Offline Facet。

比较完整 `engine.Result`、model executor 调用次数、Corpus fake 调用次数。二者
必须相同。不得启动真实 TLC 或 fuzz campaign。

## 12. Acceptance criteria

Stage 3 只有全部满足才完成：

1. 只新增 `internal/assurance/facet`、`facet/raft`、测试/fixture 和 Stage 3 报告。
2. 三个 evaluator 的 definition/class 与 Stage 2 精确一致。
3. Key typed、versioned、deterministic、finite。
4. state/transition scope 之外没有 history evaluator。
5. missing/invalid/not-applicable/evaluated 可机器区分。
6. partial/failure prefix 可评价。
7. node/message renaming 和 uniform term/index shift tests 通过。
8. 不解析 `model.State.Text`。
9. 不修改或 import Runtime/Adapter/TLC/Corpus。
10. 不读写 artifact，不接 Runner/CLI。
11. 原 Stage 0/1 与全仓 test/race/vet 通过。
12. feature-off observable execution 完全相同。

## 13. Stop conditions

出现以下情况必须停止，不扩大重构：

- 必须修改 Frozen Kernel、Trace/Observation/schema；
- 必须让 Engine/Experiment import facet；
- 必须读取 Runtime/RawNode；
- 必须解析或复制 `model.State.Text` projector；
- 必须重跑 Mapper/TLC/replay 才能分类；
- 现有 Effect schema无法表达 Catalog 的 exact predicate；
- 不能满足 node renaming 或 term/index shift invariance；
- 需要 DSL/registry/plugin/provider；
- 需要 Goal history 才能实现某个 v1 Facet；
- 需要接 CLI/Corpus/artifact loader；
- golden 与 Catalog 出现实质冲突。

这些条件触发时报告最小缺口，不修改本 Stage 2 权威文档来迎合实现。

## 14. 明确非目标

- Facet novelty/coverage union/campaign tracking
- Corpus admission/energy/parent selection
- artifact persistence/retention/loader
- historical/held-out experiment
- Goal/Waypoint/Frontier/Handoff
- Agent proposal schema、prompt 或编排
- TLA+/Mapper/Oracle 变更
- cross-facet combination
