# Semantic Assurance Kernel Boundary

## 1. 边界目标

本边界把当前 `75d4e51120b370acb880d003629f916da3f1a080` 的稳定实现分为
Frozen Kernel、Read-Only Extension Surface 和 Future Extension Layer。
原则是扩展层消费一次已经完成的确定性执行，而不是进入执行路径、读取可变
RawNode，或改变 TLC/Oracle/Corpus 的判定时机。

## 2. Frozen Kernel

除有独立最小复现、回归测试和明确授权的 baseline bug 外，后续 Semantic
Assurance 重建不修改下列部分。

| 部分 | 当前职责与稳定理由 | 后续规则 |
|---|---|---|
| `internal/core` | 定义 `Action`、`Effect`、`Observation`、`StepRecord`、`Trace`、`FailureRecord` 等协议无关执行事实；序列化和 copy/normalize 语义已被 Runtime、Replay、Mapper 共用 | 冻结。例外仅限会破坏现有确定性/校验的 baseline bug |
| `internal/runtime` | 单线程逻辑时间、受控网络、预算、adapter 调用、Observation digest、Trace/failure 捕获 | 冻结；不得加入 Facet/Goal/Agent hook |
| `internal/plan` | 高层 `PlanAction/PlanSequence`、基于最新 Observation 的 `Resolver` | 冻结解析与执行语义；若未来需要公共 Plan digest，必须是独立、兼容的窄工具且另行批准 |
| `internal/engine` | 组织 Resolver → Runtime → Mapper → Oracle → TLC，输出 `engine.Result` | 冻结；不得 import Assurance、Facet、Goal、Agent |
| `internal/adapters/etcdraft` | 封装 in-process etcd-raft `RawNode`、storage、snapshot、fault policy | 冻结；扩展层不得读取其可变内部状态 |
| `internal/model`、`internal/model/raft`、`internal/model/tlc` | Concrete transition 映射、profile/bounds、model event/state、strict TLC client | 冻结模型映射、Action/profile/bounds 和 strict 调用语义 |
| `internal/oracle`、`internal/oracle/raft` | 对初始 Observation 和每个 concrete transition 做在线检查，产生 `Finding` | 冻结；扩展结果不得覆盖或重分类 Finding |
| `internal/corpus` | 合并 raw/semantic coverage、执行既有准入、保存 mutation 所需 Plan 与增量 keys | 冻结；不得让新模式改变默认 Consider、coverage、checkpoint |
| `internal/persistence` | strict JSON、atomic JSON 写入、JSONL journal/水位读取 | 冻结默认格式和行为；未来可作为叶层 writer 的工具，但不能反向依赖 Assurance |
| `internal/minimize` | 反复调用同一 executor、稳定失败签名、ddmin、checkpoint/cache | 冻结；Record 不参与 minimization 判据 |
| `internal/trace` | concrete Trace 的严格读取与确定性 replay/divergence | 冻结；Replay 不依赖新 Record 才能继续工作 |
| `internal/experiment` | 多候选调度、mutation、aggregation、Corpus、checkpoint 与同步 hooks | 基线冻结；Stage 1 优先只消费已有 `Completion`，不在 Runner 内加入语义策略 |
| `tools/tlc-server` | controlled strict TLC Java 服务、event step、invariant/ambiguous successor 检查 | 冻结 Java、协议和 jar 版本 |
| `models/raft` | `raft.tla`、`raft_storage_snapshot.tla` 及对应 `.cfg` bounds | 完全冻结；当前研究范围内不由 Agent 生成或修改 |

当前 `go list` 的直接依赖支持上述层次：

```text
core -> stdlib
runtime -> core, sut
plan -> core
engine -> core, model, oracle, plan, runtime
corpus -> model, plan
minimize -> core, engine, plan
experiment -> core, corpus, engine, metrics, model, mutation, plan
```

任何 `engine/runtime/model/oracle/adapter -> assurance` 的新增边都违反边界。

## 3. Read-Only Extension Surface

未来扩展可以读取以下已经完成且复制/持久化安全的事实。

| 只读事实 | 当前来源 | 现状与适配需求 |
|---|---|---|
| 已完成 Plan | `experiment.FeedbackExecution.Plan`、`Completion.Candidate.Plan`、`plan.json`、`corpus.Entry.Plan` | 已有值；Record 默认保存 digest/ref，不重复整份 Plan |
| Candidate identity | `experiment.Candidate` 与 `experiment.Run` | ID/parent/source/depth/index/seed 分散，需 Stage 1 builder 组合 |
| Concrete Trace | `engine.Result.Trace`、`trace.json` | 完整对象可能大；保存 digest 和相对 artifact ref |
| Initial/Final Observation | `engine.Result.Initial/Final` | 可作为小型摘要来源；不得借此读取 Runtime mutable state |
| 每步完整 Observation | 仅执行中的 `runtime.StepResult` | 当前完成边界未保留，不属于稳定 surface；需求出现时必须停下重新评估 evidence |
| Model Event | `engine.Result.ModelEvents`、`model-events.json` | 保存 count/digest/ref，不复制完整序列 |
| Model State | `engine.Result.ModelStates`、`model-states.json` | 保留有序 Key path digest/ref；避免以 Text 作为 identity |
| TLC outcome | `engine.Result.ModelExecuted/Status/Error`，调用期 typed `tlc.ExecutionError` | 成功状态足够；失败 typed code 未完整持久化，Record 接收上游已给出的稳定摘要，不解析新规则 |
| Oracle outcome | `engine.Result.OracleFindings`、`oracle-findings.json` | 保存 count、排序后的稳定 `Oracle:Code`、artifact ref |
| Failure signature | `minimize.SignatureOf` / minimize report | 保存调用方提供的 signature/digest/ref，不在 Record 中重算 |
| Replay reference | run 目录的 `config.json` + `trace.json`，Trace ExecutionID/Seed | Stage 1 定义 typed 相对引用；不使用绝对路径参与 digest |
| Config fingerprint | `experiment.FeedbackOptions.ConfigurationFingerprint`、`experiment.Checkpoint`，由 `cmd/.../configurationFingerprint` 生成 | 作为 opaque digest 注入，不由核心结果或 Record 自行重算 |
| Corpus outcome | `experiment.Run.Retained/CorpusID/CorpusAdmission` | 已在 `Completion` 时确定，Record 可只读复制 |

推荐的只读适配器是 Stage 1 的 builder：输入现有
`experiment.Completion`/等价显式字段和 artifact references，输出版本化的紧凑
Record。它是新 package，不是 Engine hook，也不拥有 Runtime。

## 4. Future Extension Layer

这里只冻结职责和依赖方向，不实现任何代码。

### 4.1 Completed Execution Record

一次候选完全结束后，把候选身份、现有 digest/outcome 和 artifact references
组合为不可变、确定序列化的记录。它不执行、不准入、不重放。

### 4.2 Offline Facet Evaluator

只消费 Record 指向的已完成 evidence，计算冻结的语义观察。若所需数据不存在，
应报告 evidence 缺失，不得读取 Runtime/RawNode 补齐。

### 4.3 Goal Monitor

只读取完成执行或其有序 evidence 来判断 Goal/进展；不改变 Oracle、TLC 或
Corpus outcome，不在 Stage 1 实现。

### 4.4 Assurance Matrix

未来只聚合已经独立产生的 execution/Facet/Goal/evidence 结果；不得成为 Engine
中的统一 reward 或全局状态。本阶段不设计其 schema。

### 4.5 Agent Proposal Validator

Agent 未来只能产生结构化 proposal。确定性 validator 先验证 schema、边界和
可用动作，再交给既有 Plan/Resolver/Engine。Agent 不获得 Runtime/Adapter
句柄，也不直接调用执行器。

## 5. 强制单向依赖规则

1. `semantic/assurance/facet/goal/agent` 可以依赖稳定内核公开的只读数据。
2. `internal/runtime`、`internal/engine`、`internal/model`、
   `internal/oracle` 和 adapter 不得反向依赖上述扩展。
3. Facet/Goal 不得读取 etcd-raft `RawNode` 或 Runtime 可变内部状态。
4. Agent 不得直接调用 Runtime 或 Adapter。
5. Agent proposal 必须由确定性模块验证，最终仍经现有 Plan/Resolver/Engine。
6. TLA+ 模型和 Mapper 是冻结输入/验证后端，不属于 Agent 生成流程。
7. 扩展关闭或未被调用时，baseline 的 Actions、Effects、Trace、TLC 次数、
   Findings、Corpus、artifact 和 CLI 默认语义必须逐项不变。

允许方向为：

```text
Future extension
  -> read-only record / existing exported values
  -> frozen kernel
```

禁止方向为：

```text
runtime/engine/model/oracle/adapter/corpus
  -> Facet/Goal/Assurance/Agent
```

## 6. 最容易破坏边界的位置

- `internal/engine/engine.go`：把 evaluator 插进 action loop 会改变时机、错误传播
  或 TLC 次数。
- `internal/runtime/action.go` 和 `internal/adapters/etcdraft`：为了“方便观察”
  暴露 pending queue/RawNode 指针会泄漏可变状态。
- `internal/model/raft/mapper.go`：加入 Facet/Goal 字段会把协议映射和搜索语义
  绑定。
- `internal/experiment/runner.go`：它已经横跨调度、mutation、Corpus 和 hooks；
  将新决策塞进 Runner 容易形成第二套 admission。
- `internal/corpus/corpus.go`：未来 Facet admission 若直接修改 `Consider`，会
  破坏默认 coverage/dedup/checkpoint。
- `cmd/modelfuzz-ng`：CLI 同时构建所有组件并写 artifact，是最容易产生全局
  wiring 和默认行为漂移的位置。Stage 1 不修改它。
- `internal/persistence`：为新 schema 修改通用 JSON 行为会影响 checkpoint、
  replay、minimize；新 Record 应复用而非改变。

## 7. Baseline bug 例外

“未来扩展需要更多字段”不构成修改 Frozen Kernel 的理由。只有同时满足下列条件
才可例外：

1. 能在没有 Facet/Goal/Agent 的 baseline 中最小复现；
2. 违反当前已有类型、测试、Replay、TLC 或 Oracle 契约；
3. 修复范围最小且有回归；
4. 由单独任务明确授权。

否则应在扩展层报告 evidence 缺失或停止，而不是扩大内核。
