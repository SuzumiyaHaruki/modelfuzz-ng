# Stage 1：Completed Execution Record 规格

## 1. 唯一目标

Stage 1 只新增一个只读、版本化、可确定序列化的“已完成执行记录”。推荐名称：

```text
package: internal/assurance/executionrecord
type:    CompletedExecutionRecordV1
schema:  modelfuzz-ng-completed-execution-v1
major:   1
```

它让未来 Facet、Goal 和 Agent proposal validator 读取同一份完成事实，而无需
访问或修改 Runtime、Adapter、Mapper、TLC、Oracle 和 Corpus 内部状态。

它不是新执行结果、artifact 平台或实验框架；它是现有结果的紧凑索引。

## 2. 构建边界

Record 只能在以下全部已确定后构建：

1. `engine.Engine.Run/RunSource` 已返回；
2. Mapper、strict TLC 和 Oracle 已按照原路径完成或给出原失败；
3. 对 experiment candidate，`corpus.Corpus.Consider` 已完成；
4. `experiment.Completion` 中 Run/Candidate/Execution 已就绪；
5. 调用方已决定哪些既有 artifacts 实际存在并提供其相对引用与 SHA-256。

推荐 API 形式：

```go
type BuildInput struct {
    Completion        experiment.Completion
    ConfigFingerprint string
    Artifacts         ArtifactReferences
    FailureSignature  *minimize.Signature // 或调用方提供的稳定摘要
}

func BuildV1(input BuildInput) (CompletedExecutionRecordV1, error)
```

如果直接依赖 `experiment.Completion` 造成 package cycle，等价的最小方案是让
`BuildInput` 显式接收现有 `Candidate`、`Run`、`PlanSequence` 和
`engine.Result`。不得为解决 cycle 复制这些完整类型，也不得让 Engine import
新 package。

## 3. 推荐字段与来源

以下是语义字段；具体 Go 字段名可以按仓库惯例微调，但不得扩大职责。

| 字段组 | 来源 | 保存方式 |
|---|---|---|
| `schema_id`, `major_version` | 本规格常量 | 值 |
| `record_digest` | 本记录冻结的 canonical digest payload | 完整小写 SHA-256 |
| candidate ID/kind/parent/source/depth/run index/seed | `experiment.Candidate`、`experiment.Run` | 值 |
| `plan_digest`, action count | 优先复用 `experiment.Run.PlanDigest` 和 `len(FeedbackExecution.Plan.Actions)` | digest + count；可选相对 `plan.json` ref |
| execution status/error class | `engine.Result.Status`、termination、code、budget | 稳定枚举/码；自由文本仅 debug 且不进 digest |
| runtime termination | `engine.Result.Termination/TerminationCode`、Trace steps、Actions count | 值 |
| TLC/model outcome | `ModelExecuted`、status、ModelEvents/States counts、`Run.ModelStatePathDigest` | 值/digest/ref |
| Oracle outcome | `engine.Result.OracleFindings` | finding count、排序去重的 `Oracle:Code`、相对 ref |
| Concrete Trace | `engine.Result.Trace`、`Run.TraceDigest`、`trace.json` | digest、step count、相对 ref，不内嵌 |
| Model Events/States | `engine.Result` 和既有 JSON artifacts | count、state-path digest、相对 refs，不内嵌 |
| replay reference | run 的 `config.json` + `trace.json`、Trace ExecutionID/Seed | typed 相对 refs + digest/identity |
| failure signature | 调用方已通过 `minimize.SignatureOf` 或等价基线逻辑取得 | typed value或 digest/ref；Record 不重算 |
| failure artifact | `failure.json` / minimize report | 相对 ref + SHA-256 |
| config fingerprint | `FeedbackOptions/Checkpoint.ConfigurationFingerprint` | opaque SHA-256 |
| Corpus outcome | `experiment.Run.Retained/CorpusID/CorpusAdmission` | 值；只读，不重新判定 |

`ArtifactReference` 至少包含 `kind`、repository/run-root 相对路径和文件 SHA-256。
它不得接受绝对路径、`..` 逃逸、空 digest 或未知 kind。路径用于定位，不进入
record semantic digest；文件 digest 用于校验 evidence。

## 4. 不保存的内容

Record 不再次内嵌：

- `plan.PlanSequence`；
- `core.Trace`；
- 全部 `core.Observation`；
- `[]model.Event`、`[]model.State`；
- `[]oracle.Finding`；
- snapshot/message payload；
- goroutine stack 或完整日志；
- Corpus entry；
- checkpoint；
- mutable Runtime/Adapter 句柄。

如果调用场景没有持久化 Plan/Trace/Model artifacts，Record 可以表达引用缺失，
但不得谎称 replayable。未来 Facet 的 evidence requirement 应显式拒绝不充分的
Record。

## 5. 序列化与确定性

- 格式：UTF-8 JSON，顶层使用 typed struct，不以 `map[string]any` 作为 schema。
- schema/version：每条记录显式包含；未知 major version 显式拒绝。
- slice 顺序：
  - Oracle codes 按字典序去重；
  - artifact references 按 `kind` 再按相对路径排序；
  - 其他集合在进入 Record 前排序；
  - 有语义顺序的 state path 不排序，只保存现有 path digest。
- decoder 使用 strict unknown-field 检查，沿用
  `internal/persistence.ReadJSONStrict` 的行为。
- builder defensive-copy 所有 slice/string summary；调用方之后修改输入不得
  改变 Record。

### 5.1 Stable digest payload

`record_digest` 对一个专用 typed payload 做 `encoding/json.Marshal`，再完整
SHA-256。payload 仅含：

- schema ID/major；
- candidate 稳定身份、run index/seed；
- plan digest/action count；
- execution status、termination、稳定 code、counts；
- trace digest/step count；
- model executed、event/state counts、state-path digest；
- 排序后的 Oracle codes；
- failure signature digest（如有）；
- config fingerprint；
- Corpus outcome。

明确排除：

- wall-clock time、duration；
- PID、hostname；
- 临时/绝对/相对路径；
-日志位置；
-人类错误全文；
- pointer 地址；
-非确定 map 顺序；
- artifact 实际落盘顺序。

Record JSON 本身可包含 debug error 或 artifact path，但这些字段必须标明
“不参与 Stable digest”。

### 5.2 现有 digest 兼容

Stage 1 不新造 Plan/Trace/StatePath 算法。当前算法位于
`internal/experiment/digest.go` 的非导出 `digestPlan/digestTrace/digestStatePath`：

- Plan digest：仅 JSON 编码 `PlanSequence.Actions`；
- Trace digest：仅 Trace Version + Steps，排除 ExecutionID/Seed/Metadata；
- State path：按顺序编码 `State.Key`，保留重复。

首选直接使用已经写入 `experiment.Run` 的三个 digest。若非实验调用方需要构建
Record，则由调用方显式提供已经验证的 digest；Stage 1 不顺便抽取公共 identity
API。若同一 Plan 的 `internal/minimize.planDigest` 与 experiment digest 被测试
证明不兼容，应停止而不是强行统一。

## 6. Outcome 表达

Record 必须保持上游分类，不将错误转换为 Facet/Assurance 状态：

- `execution_status` 原样使用 `engine.Status`；
- `model_attempted/model_executed` 与 engine 事实一致；
- Oracle finding code 是结果，不由 Record 解释；
- pre-execution/invalid/resolution/runtime/mapping/model/oracle/policy failure
  分开；
- `replayable` 只在 config/trace refs 和必要 identity 都存在时为 true；
- failure signature 缺失与“没有 failure”必须区分。

Record builder 返回的 validation error 是记录基础设施错误，不改写原
`engine.Result`、failure signature 或 Corpus outcome。

## 7. 预计文件修改

Stage 1 预计只新增：

```text
internal/assurance/executionrecord/record.go
internal/assurance/executionrecord/builder.go
internal/assurance/executionrecord/digest.go
internal/assurance/executionrecord/record_test.go
internal/assurance/executionrecord/builder_test.go
```

允许根据实现把三个生产文件合并为两个，但不新增通用 registry、writer、
observer 或 provider。预计不修改任何已有生产文件，不修改 CLI、artifact、
Engine、Experiment、Corpus、Replay 或 Minimize。

若仅靠显式 `BuildInput` 无法提供 candidate/run/config/artifact facts，Stage 1
应报告缺失调用点，不得自动改造 `experiment.Runner`。真正的落盘/wiring 应是
后续独立阶段。

## 8. 测试职责

建议测试名/职责：

1. `TestBuildV1FromCompletedFeedbackExecution`：完整成功候选字段来源正确。
2. `TestBuildV1PreservesEachEngineStatus`：所有现有 status/termination 不重分类。
3. `TestBuildV1ModelAndOracleSummary`：TLC counts/path digest 与排序 Oracle codes。
4. `TestBuildV1FailureSignatureIsCallerSupplied`：不重新解析 error 文本。
5. `TestBuildV1RejectsIncompleteIdentity`：candidate、Plan、config digest 缺失。
6. `TestBuildV1ArtifactReferencesAreRelativeAndTyped`：拒绝绝对路径/逃逸/坏 SHA。
7. `TestRecordV1JSONRoundTripStrict`：确定编码、unknown field/version 拒绝。
8. `TestRecordV1DigestDeterministic`：相同输入至少重复 20 次相同。
9. `TestRecordV1DigestIgnoresDebugAndPaths`：时间、路径、debug text 不改变 digest。
10. `TestRecordV1DigestChangesOnSemanticOutcome`：Plan/Trace/status/code 变化可见。
11. `TestRecordV1DefensiveCopies`：修改输入/返回 accessor 不污染 Record。
12. `TestExistingExperimentDigestsRemainCompatible`：用现有 experiment Run golden
    比较，而不是重写 expected。
13. `TestBuildV1DoesNotMutateExecutionResult`：构建前后 `engine.Result` 深比较。
14. `TestFeatureOffExecutionEquivalence`：固定 Plan 分别“不调用 builder”和“执行
    完成后调用 builder”，Actions、Effects、Trace、Events、States、Findings、
    TLC 调用数和 status 完全一致。

测试不得启动新 fuzz campaign；集成等价性优先复用现有 Engine fake/strict TLC
短时 harness。

## 9. Feature-off 约束

Stage 1 package 不被调用时没有任何行为变化。即便调用，也只能发生于执行结束
之后，并且：

- 不增加或减少 Runtime action；
- 不改变 Mapper/TLC 次数；
- 不改变 Oracle findings；
- 不调用 `Corpus.Consider`；
- 不触发 mutation；
- 不写默认 artifact；
- 不影响 Replay/Minimize；
- 不改变 checkpoint/schema/CLI。

## 10. Acceptance criteria

Stage 1 仅在以下全部满足时完成：

1. 新 package 是消费现有完成结果的叶层；
2. schema/version 明确且 strict decode；
3. record digest 确定、完整 SHA-256；
4. 没有完整 Plan/Trace/State/Finding 重复副本；
5. candidate/Plan/execution/TLC/Oracle/replay/failure/config 信息均可表达；
6. artifact refs typed、相对、带 digest；
7. builder 不修改输入；
8. feature-off/on 的 baseline 可观察执行相同；
9. 全仓 `go test ./...`、`go test -race ./...`、`go vet ./...`、
   `git diff --check` 通过；
10. 没有实现 Facet、Goal、Waypoint、Frontier、Handoff、Matrix、Agent、CLI、
    admission、retention 或新实验框架。

## 11. 明确非目标

- 不计算 Facet/Goal；
- 不决定 Corpus admission；
- 不持久化 Record；
- 不新增 artifact policy；
- 不启动 TLC 或 replay；
- 不取代 `engine.Result`、`experiment.Run` 或 `Completion`；
- 不抽象跨协议 executor；
- 不设计 Agent 编排或 Assurance Matrix；
- 不补存每一步完整 Observation；
- 不修改 TLA+/CFG/Mapper/Oracle。

## 12. 必须停止的情况

遇到以下任一项，Stage 1 应停止并报告，而不是扩大重构：

1. 构建 Record 必须让 Engine/Runtime/Mapper/TLC/Oracle/Adapter import Assurance；
2. 必要事实只能从 mutable RawNode/Runtime 内部读取；
3. 必须修改 Action、Trace、Model State 或 Finding schema；
4. 现有 experiment/minimize 的 Plan digest 对相同语义输入不兼容；
5. config fingerprint 无法由调用方提供，且修复要求改写 CLI/Experiment；
6. replay identity 无法用现有 config/trace/ExecutionID/Seed 表达；
7. Facet 需求强制持久化当前不存在的完整中间 Observation；
8. 需要改变默认 artifact、Corpus、checkpoint、Replay 或 Minimize 行为；
9. 需要引入通用 plugin/registry/provider；
10. baseline 测试或 race 在未修改代码时失败且原因未确认。

这些都应成为独立、可复现、最小授权的后续任务，而不是 Stage 1 的附带重构。
