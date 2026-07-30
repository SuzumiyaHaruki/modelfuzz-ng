# Stage 1 Completed Execution Record v1 实现报告

## 1. 结论

Stage 1 已实现为独立叶层 package：

```text
internal/assurance/executionrecord
```

固定 schema 为 `modelfuzz-ng-completed-execution-v1`，major version 为 `1`。
它只在候选、Corpus 判定和 artifact 选择全部结束后，组合
`experiment.Completion` 与调用方提供的只读引用；没有被 Runner、Engine、CLI
或 Artifact Store 调用。

没有触发任何要求停止的语义阻塞。实现前全仓 race 曾暴露一个既有 experiment
取消时序测试的单次波动；该测试单独 race 重复 20 次通过，最终实现后的全仓
race 也通过，详情见 `COMMAND_RESULTS.md`。

## 2. 实际新增文件

生产代码：

```text
internal/assurance/executionrecord/record.go
internal/assurance/executionrecord/builder.go
internal/assurance/executionrecord/digest.go
internal/assurance/executionrecord/codec.go
```

测试：

```text
internal/assurance/executionrecord/builder_test.go
internal/assurance/executionrecord/codec_test.go
internal/assurance/executionrecord/equivalence_test.go
```

报告：

```text
docs/semantic-assurance-rebuild/stage1/STAGE1_IMPLEMENTATION_REPORT.md
docs/semantic-assurance-rebuild/stage1/COMMAND_RESULTS.md
```

没有修改 Stage 0 文档或任何 Frozen Kernel 文件。

## 3. 公开 API

主要入口：

```go
func BuildV1(input BuildInput) (CompletedExecutionRecordV1, error)
func DecodeV1(reader io.Reader) (CompletedExecutionRecordV1, error)
```

公开 schema 类型包括：

- `CompletedExecutionRecordV1`
- `BuildInput`
- `ArtifactKind` / `ArtifactReference`
- `FailureSignatureAvailability` / `FailureSignatureInput`
- typed summary：`CandidateIdentity`、`PlanSummary`、`EngineOutcome`、
  `ExperimentOutcome`、`TraceSummary`、`ModelSummary`、`OracleSummary`、
  `FailureSummary`、`CorpusOutcome`、`ReplaySummary`
- `ErrInvalidRecord`

`ArtifactReference.Validate` 只验证引用格式，不访问文件系统。

## 4. 字段来源

| Record 部分 | 唯一来源/处理 |
|---|---|
| schema/version | package 常量 |
| candidate ID/kind/parent/source/depth | `Completion.Candidate`，并与 `Completion.Run` 交叉校验 |
| run index/seed | `Completion.Run` |
| Plan digest | 原样消费 `Completion.Run.PlanDigest` |
| Plan action count | `len(Completion.Execution.Plan.Actions)` |
| Engine outcome | 仅 `Completion.Execution.Result` |
| Experiment outcome | 仅 `Completion.Run` |
| Trace digest | 原样消费 `Completion.Run.TraceDigest` |
| Trace version/ID/seed/step count | `Completion.Execution.Result.Trace` |
| Model counts/executed | `Completion.Execution.Result` |
| State path digest | 原样消费 `Completion.Run.ModelStatePathDigest` |
| Oracle finding count/codes | `Result.OracleFindings`，仅提取并排序去重 `Oracle:Code` |
| Failure signature | 仅调用方的 `FailureSignatureInput` |
| Corpus outcome | `Run.Retained/CorpusID/CorpusAdmission` |
| config fingerprint | 调用方 opaque SHA-256 |
| artifact references | 调用方列表，格式校验、复制、排序 |
| Replayable | Builder 从 Trace identity、Run seed/digest 和 config/trace references 推导 |

Record 不内嵌 Plan、Trace、Observation、Model Event/State、Finding、Corpus Entry
或 Checkpoint。

## 5. EngineOutcome 与 ExperimentOutcome

两层 outcome 使用独立 typed struct：

- `EngineOutcome` 保存 `engine.Result` 的 status、debug error、
  `ModelExecuted`、budget/termination 和各项执行计数；
- `ExperimentOutcome` 保存 `experiment.Run` 的 Completed、Status、
  Succeeded 和 debug error。

Builder 不要求 `Run.Status == Result.Status`。测试
`TestBuildV1PreservesEngineAndExperimentOutcomeDivergence` 固定验证
Engine `completed` 与 Experiment `mapping_failed` 可以同时保留。

`Result.Error`、`Run.Error` 和 `TerminationDetail` 只作为 debug text 保存，
均不进入 RecordDigest。

## 6. Failure Signature 三态

封闭枚举为：

```text
not_applicable
unavailable
available
```

- `not_applicable` 与 `unavailable` 要求 pointer 为 nil；
- `available` 要求 pointer 非 nil，且只能用于非 completed/canceled 的失败结果；
- Signature 由调用方提供；Builder 不调用 `minimize.SignatureOf`，不解析任何
  error text；
- Builder defensive-copy Signature，把 `OracleCodes` 排序去重，再对规范化的
  `minimize.Signature` JSON 计算完整 SHA-256；
- decoder 重新验证三态、canonical OracleCodes 和 Signature digest。

## 7. RecordDigest

算法：

```text
typed digest payload
→ encoding/json.Marshal
→ SHA-256
→ 64 位小写十六进制
```

包含：

- schema ID/major；
- candidate identity、run index、seed；
- Plan digest/action count；
- Engine stable status、model/budget/termination code 和 counts；
- Experiment Completed/Status/Succeeded；
- Trace digest/version/step count；
- Model executed/counts/state-path digest；
- canonical Oracle codes；
- Failure availability/signature digest；
- configuration fingerprint；
- Corpus retained/ID/admission。

排除：

- RecordDigest 自身；
- Engine/Run debug error；
- TerminationDetail；
- duration、wall clock、PID、hostname；
- Artifact path 和文件 SHA；
- Replayable；
- Trace ExecutionID/Trace Seed；
- pointer、map iteration、日志或写入顺序。

因此 RecordDigest 是某一已完成候选记录的身份；Plan/Trace/StatePath 等价仍使用
现有 `experiment.Run` digests。package 未复制、导出或重算
`internal/experiment/digest.go` 的算法。测试通过公开 `Runner.RunFeedback`
产生真实 Run digests，再证明 Builder 原样消费。

## 8. ArtifactReference

固定 kind 仅覆盖当前已有引用：

```text
config
plan
trace
model_events
model_states
oracle_findings
failure
result
candidate
run_summary
minimize_report
```

规则：

- path 使用 slash 语义和 `path` 包；
- 拒绝空路径、绝对路径、UNC、Windows drive、反斜杠、任何 `..` component
  以及非 canonical path；
- SHA 必须是完整 64 位小写十六进制；
- 拒绝重复 `kind + path`；
- 输出按 kind、path 排序并复制；
- 不读取目标文件；
- 不比较文件 SHA 与 Plan/Trace/StatePath semantic digest。

所有引用保存在 canonical 顶层 `Artifacts`；各 summary 的便利指针由该列表
确定性派生。一个 kind 有多个不同路径时，便利指针为空以避免任意选取，原始
canonical references 仍完整保留。

## 9. Replayable 推导

只有同时满足以下条件才为 true：

- 恰好一个合法 config reference；
- 恰好一个合法 trace reference；
- Trace version 为 `core.CurrentTraceVersion`；
- Trace ExecutionID 合法；
- Trace seed 与 Run seed 相同；
- Run.TraceDigest 是合法 SHA-256。

Replay summary 中的 config/trace references、ExecutionID 和 seed 均由 Builder
派生，decoder 会重新推导并比较。本阶段不读取文件或实际执行 replay。Artifact
文件 SHA 和 Trace semantic digest 明确允许不同。

## 10. Strict decode

`DecodeV1`：

1. 读取输入并拒绝非法 UTF-8；
2. 使用 `json.Decoder` 和 `DisallowUnknownFields`；
3. 拒绝第二个/trailing JSON value；
4. 校验 schema/version 和全部封闭枚举；
5. 校验所有 SHA、counts、三态与 summary 一致性；
6. 校验 canonical Oracle codes 和 artifact 顺序/去重；
7. 重新派生 summary artifact pointers 和 Replayable；
8. 重新计算 RecordDigest；
9. 返回独立 defensive copy。

没有新增 writer，也没有修改 `internal/persistence`。

## 11. 不可变性与输入保护

Builder 在规范化前复制 artifact slice；Failure Signature、OracleCodes 和所有
Record slice/pointer 也独立复制。构建后修改：

- `BuildInput.Artifacts`
- caller Signature/OracleCodes
- Completion Plan、Findings 等 slice

不会改变 Record。`TestBuildV1DoesNotMutateCompletion` 还比较构建前后的完整
调用方输入快照，确认 Builder 没有就地排序或写入。

## 12. 依赖方向

生产 package 的仓库内直接依赖只有：

```text
internal/core
internal/engine
internal/experiment
internal/minimize
internal/oracle
```

以及 Go 标准库。`rg` 确认任何既有 `internal` package 都没有 import
`executionrecord`。因此依赖仍为：

```text
executionrecord -> Frozen Kernel 的只读公开类型
```

没有反向依赖、package cycle、global state、文件系统或网络访问。

## 13. 测试矩阵

测试覆盖：

- 完整 Completion 构建和所有字段来源；
- Engine/Experiment outcome divergence；
- 当前十种 Engine status；
- model failure 时 `ModelExecuted=true`；
- Model/Oracle summary；
- Failure Signature 三态、错误组合、copy/digest；
- Artifact kind/path/SHA/order/duplicate；
- Replayable 全部必要条件；
- JSON round trip、unknown field/schema/version/trailing JSON/非法 UTF-8；
- non-canonical slice 和 RecordDigest mismatch；
- 相同输入重复 20 次；
- debug/artifact/replay 非语义字段不改变 digest；
- 所有冻结身份/outcome 字段改变 digest；
- Completion 的全部基础一致性检查；
- Builder defensive copy 与不修改输入；
- 公开 `Runner.RunFeedback` digest 消费；
- 两个独立相同 Engine/fake executor 的 feature-off/on 完整结果与调用次数等价。

最终单包 statements coverage 为 83.7%。生产代码 4 个文件、911 行；测试
3 个文件、857 行。规模主要来自 typed schema、逐字段 validation 和完整的
确定性/一致性测试，没有通用框架代码。

## 14. 与规格的偏差和限制

没有语义偏差。实现选择了：

- 四个生产文件，而不是再创建公共 digest/validator package；
- 顶层 canonical Artifact 列表加 summary 派生指针；
- 对 `available/unavailable` 额外确认 Engine 结果不是 completed/canceled；
- decoder 显式拒绝非法 UTF-8。

已知限制：

1. 不验证 artifact 是否存在或其文件 SHA 是否匹配磁盘；
2. 不验证 Run semantic digests 是否与完整对象对应，而是按约束信任 Runner；
3. 不执行 replay；
4. 不写 Record；
5. Trace 无合法 replay identity 时仍可形成非 replayable Record；
6. 新增 Engine/Candidate enum 需要未来提升或明确更新 schema validator。

这些限制均符合 Stage 1 的只读索引范围。

## 15. 范围确认

- 未实现或接入 Facet、Goal、Waypoint、Frontier、Handoff；
- 未实现 Assurance Matrix、Agent 或 proposal；
- 未修改 Engine、Runtime、Adapter、Mapper、TLC、Oracle、Corpus、
  Experiment、Persistence、Replay、Minimize、CLI、TLA+ 或 Java；
- 未自动写 `completed-execution.json`；
- 未运行真实 TLC 服务、fuzz campaign 或长时间实验；
- 未执行 git add、commit、push、reset、clean、rebase、cherry-pick 或分支切换。

Stage 1 到此停止，不进入 Stage 2。
