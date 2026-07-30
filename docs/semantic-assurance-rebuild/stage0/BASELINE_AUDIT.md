# 稳定基线审计

## 1. 审计结论

当前稳定内核已经有一条确定的单候选闭环：

```text
plan.PlanSequence / plan.PlanAction
  -> plan.Resolver.Resolve（基于最新 Observation 在线解析）
  -> core.Action
  -> runtime.Runtime.Execute
  -> sut.Adapter / adapters/etcdraft.Adapter
  -> runtime.StepResult
  -> core.StepRecord / core.Trace
  -> model.Transition
  -> model/raft.Mapper.Map
  -> model.Event
  -> model/tlc.Client.Execute（整条事件序列一次提交）
  -> model.State
  -> oracle.Checker
  -> experiment.RunFeedback / corpus.Corpus.Consider
  -> persistence、Replay、Minimize
```

`engine.Result` 已经是核心执行层最接近“完成候选结果”的统一类型，但它不包含
来源 Plan、候选调度身份、配置指纹和 artifact/replay 引用。这些信息目前分散
在 `experiment.FeedbackExecution`、`experiment.Run`、`experiment.Candidate`
和 CLI artifact store 中。Stage 1 的最小缺口不是重构 Engine，而是在执行完成
后组合这些现有事实的只读、版本化记录。

## 2. 单候选实际调用链

### 2.1 Plan 与在线解析

- 高层动作：`internal/plan/action.go` 的 `plan.PlanAction`。
- 高层序列：`internal/plan/sequence.go` 的 `plan.PlanSequence`。
- 在线解析：`internal/plan/resolver.go` 的
  `(*plan.Resolver).Resolve(PlanAction, core.Observation) plan.Resolution`。
- 解析器每次读取上一条 concrete action 后的新 `Observation`，返回零个或多个
  `core.Action`，而不是预先固化全部消息位置和逻辑时间。

固定 Plan 由 `engine.Engine.Run` 消费；在线策略由
`engine.Engine.RunSource` 通过 `engine.ActionSource.Reset/Next` 消费。

### 2.2 Runtime 与 etcd-raft

- Concrete Action：`internal/core/action.go` 的 `core.Action` 和
  `core.ActionSequence`。
- 执行入口：`internal/runtime/action.go` 的
  `(*runtime.Runtime).Execute(context.Context, core.Action)`。
- Runtime：`internal/runtime/runtime.go` 的 `runtime.Runtime`，负责逻辑时间、
  受控消息网络、预算、Trace 和 failure 捕获。
- 稳定适配接口：`internal/sut/adapter.go` 的 `sut.Adapter`。
- 具体实现：`internal/adapters/etcdraft/adapter.go` 的
  `etcdraft.Adapter`，封装进程内 etcd-raft `RawNode`。可变 node/RawNode
  状态不对上层暴露。

`Runtime.Execute` 校验动作和预算，调用 adapter，取得 Effect 与新的
Observation，计算 observation digest，并把成功步骤追加为
`core.StepRecord`。Runtime 错误前的成功前缀仍可由 `Runtime.Trace` 取得。

### 2.3 Trace、Mapper、TLC 与 Oracle

- Effect：`internal/core/effect.go` 的 `core.Effect`；
  timer 反馈为 `core.TimerFired`，adapter model event 为
  `core.ModelEvent`。
- Observation：`internal/core/observation.go` 的 `core.Observation`、
  `NodeObservation`、`MessageObservation`。`Observation.Normalized` 对节点和
  消息排序。
- Trace：`internal/core/trace.go` 的 `core.Trace` 与 `core.StepRecord`。
  Step 保存 Action、Effect、逻辑时间、节点前后快照和 Observation digest；
  它不保存每步完整 pending-message Observation。
- Runtime failure：`internal/core/failure.go` 的 `core.FailureRecord`。
- Mapper 输入：`internal/model/transition.go` 的 `model.Transition`；
  `model.TransitionFromRecord` 可从 StepRecord 重建节点快照型 transition，
  但不能恢复已省略的完整消息队列。
- Mapper 接口：`internal/model/mapper.go` 的 `model.Mapper`；
  Raft 实现为 `internal/model/raft/mapper.go` 的 `raft.Mapper.Map`。
- Model Event：`internal/model/event.go` 的 `model.Event`。
- Model State：`internal/model/executor.go` 的 `model.State{Text, Key}`。
- Oracle Finding：`internal/oracle/oracle.go` 的 `oracle.Finding`；
  Raft checker 为 `internal/oracle/raft/checker.go` 的
  `(*Checker).Check(model.Transition)`。

`internal/engine/engine.go` 的 `Engine.run` 在每条 concrete action 后构造
`model.Transition`、调用 Mapper，并立即让 Oracle 检查 concrete transition。
候选循环结束后，`Engine.capture` 汇总 Trace；若配置了 model executor，
才调用 `model.Executor.Execute(ctx, result.ModelEvents)`。

strict TLC 不是每事件一次 HTTP 请求。`internal/model/tlc/client.go` 的
`Client.Execute` 将本候选完整 `[]model.Event` 加上 Reset 后一次 POST 到
`/execute`；Java `tools/tlc-server/.../StrictTLCServer.java` 在服务内逐事件
受控推进并返回状态序列。因而它是“逐轨迹一次提交、服务内逐事件验证”。

### 2.4 Coverage、Corpus 与完成回调

反馈实验入口是 `internal/experiment/runner.go`：

- `experiment.Candidate` 表示调度任务；
- `experiment.FeedbackExecution` 把 `engine.Result` 与实际
  `plan.PlanSequence` 放在一起；
- `experiment.Run` 是紧凑统计和 digest；
- `experiment.Completion` 在 Corpus 判定后同时携带 Run、Candidate 和
  不序列化的 FeedbackExecution。

`Runner.RunFeedback` 完成一次真实执行后，投影 model states/events，
调用 `internal/corpus/corpus.go` 的 `Corpus.Consider`。Corpus 原子合并
覆盖；即使本候选未被保留，已观察覆盖仍会合并。随后同步调用
`Hooks.OnRunComplete`，此时 `Run.Retained` 和 `CorpusID` 已定。

## 3. 是否已有统一结果类型

核心执行层有统一的 `engine.Result`：

- `Status`、`Error`、model 是否执行；
- budget/termination；
- Resolutions、Concrete Actions、Trace；
- ModelEvents、ModelStates；
- OracleFindings、Failure；
- Initial/Final Observation。

但全系统没有一个同时覆盖“候选身份 + Plan + 完整执行结果 + corpus outcome +
artifact references + config fingerprint”的统一、版本化记录。现有多个 result
类型并非完全重复，而是不同作用域：

| 类型 | 作用域 |
|---|---|
| `runtime.StepResult` | 单条 concrete action |
| `engine.Result` | 单候选核心执行 |
| `trace.Result` | concrete Trace 重放 |
| `experiment.FeedbackExecution` | Plan 与执行结果的内存组合 |
| `experiment.Run` | 实验级紧凑统计/digest |
| `experiment.Completion` | Corpus 判定后的同步回调视图 |
| `minimize.Result` / `CachedExecution` | ddmin 与稳定失败验证 |

风险在于未来模块若分别读取这些类型并重新计算身份，容易形成不一致的“完成结果”
结构。Stage 1 应组合而非替代它们。

## 4. Engine 的公开输入与输出

`engine.New` 输入 Runtime、Resolver、Mapper、可选 `model.Executor`、Engine
Config 和 Oracle Checkers。`Run` 输入 `plan.PlanSequence`；
`RunSource` 输入 `engine.ActionSource` 和动作预算。二者输出
`(engine.Result, error)`。

即使返回 error，Result 也尽量保留已完成的 Resolution、Action、Trace、
ModelEvent、Finding 和 Failure。缺口是 Result 没有：

- 原始/实际 Plan；
- experiment Candidate ID、parent、source、depth、run index；
- `experiment.Run` 的 Plan/Trace/StatePath digest；
- configuration fingerprint；
- 已落盘 artifact/replay reference；
- 独立、typed 的持久 TLC outcome（TLC 的 `ExecutionError` 在调用时 typed，
  但 `engine.Result.Error` 只保存字符串）。

## 5. 内存信息与已写 artifact

普通 `run` 由 `cmd/modelfuzz-ng/output.go` 的 `writeArtifacts` 写：

```text
config.json
plan.json
resolutions.json
actions.json
trace.json
model-events.json
model-states.json
oracle-findings.json
failure.json
result.json
```

`result.json` 又内嵌大部分上述结果，因此当前 full artifact 存在重复数据。
`core.Message` 的 payload 标为 `json:"-"`，持久化的是 payload digest 和协议
元数据；Stage 1 不应另写 snapshot payload。

实验由 `cmd/modelfuzz-ng/experiment_store.go` 维护根级：

```text
config.json
policy-config.json
experiment-settings.json
runs.jsonl
corpus.jsonl
progress.jsonl
checkpoint.json
corpus.json
experiment-report.json
experiment-metrics.json
```

以及按 artifact policy（`all`、`retained`、`failures`、`summary`）保留的 run
目录。完整 run 目录另含 `candidate.json`、`run-summary.json` 和上述 full
artifacts。选择 `summary` 或 `retained` 时，未保留的普通候选不会都有完整
Trace/Model State；因此“对历史所有候选做任意 offline Facet”目前并不总能
仅靠磁盘完成。执行时的 `engine.Result` 则拥有 ModelEvents/States、初终
Observation 和 Trace。

每步完整 `runtime.StepResult.Observation` 只存在于执行中的内存；Trace 只留
节点前后状态、Effects 与 observation digest。若未来 Facet 必须读取每一步
完整 pending-message 集合，现有 artifact 不足。不能让 Stage 1 Record 虚构或
复制不存在的数据；这应成为 Facet 需求与 artifact 保留策略的独立门禁。

## 6. Replay

`internal/trace/replay.go` 的 `trace.Replayer.Replay` 使用已配置相同的 Runtime，
按 `core.Trace.Steps[*].Action` 重新执行，逐步比较：

- trace `ExecutionID` 和 `Seed`；
- `time_before`；
- `nodes_before`；
- `time_after`、Action、Effects、`nodes_after`；
- `observation_digest`。

CLI `replay` 默认从 trace 所在目录读取 `config.json`，并由
`writeReplayArtifacts` 写 `expected-trace.json`、`actual-trace.json` 和
`replay-result.json`。所以可重放引用的最小事实是配置引用、Trace 引用及其
digest、ExecutionID/Seed；不是 Corpus entry。

## 7. Minimize 与失败签名

`internal/minimize/minimize.go` 的 `Reduce` 接收原 Plan 和
`minimize.Execute` callback。CLI 为每次尝试重新构造相同配置的 Engine。
`SignatureOf(engine.Result)` 形成：

- engine status；
- Failure kind/operation/code/panic；
- TLC code/action；
- mapping/runtime 错误类；
- 排序后的 `oracle:code`；
- termination code。

Reducer 先验证基线 signature 稳定，再执行 ddmin 和单动作固定点，最终重复
验证最小结果。checkpoint v1 保存 Original/Current Plan、完整 baseline/current
`engine.Result`、Signature、attempts、输入/config SHA 和执行 cache。

当前 signature 可直接作为 Stage 1 的外部输入或稳定引用，不应在 Record 包中
复制字符串解析规则。风险是部分错误分类仍从 `engine.Result.Error` 文本解析；
未来若需升级 typed outcome，应独立修 baseline bug，而不是由 Assurance 猜测。

## 8. Corpus 与 checkpoint/resume

`corpus.Entry` 保存：

- ID、ParentID、Source、Depth、RunIndex、Seed；
- 完整 `plan.PlanSequence`（供后续 mutation）；
- 本次新增的 raw/semantic state/transition keys。

它不保存 Concrete Actions、Trace、完整访问状态或 artifact reference。
`corpus.Snapshot` 保存 coverage sets 和 Entries；实验 checkpoint 中的
`corpus.Checkpoint` 只保存 coverage sets 与 `EntryCount`，完整 entries 由
`corpus.jsonl` 提供。

`internal/experiment/lifecycle.go` 的 `experiment.Checkpoint` v1 保存：

- experiment config 和 aggregation snapshot；
- corpus checkpoint；
- ready/in-flight/pending-mutation 调度状态；
- ID、run、event 和 elapsed counters；
- `ConfigurationFingerprint`；
- wall-clock `SavedAt`（它是恢复元数据，不应进入语义 digest）。

恢复时以 checkpoint 水位校验 `runs.jsonl` 和 `corpus.jsonl`。它不是完成执行
记录，也不包含所有 Trace。

## 9. 可供未来只读消费的边界

已有最接近稳定边界的是 Corpus 决策后的 `experiment.Completion`：

- `Completion.Candidate`：候选来源身份与 Plan；
- `Completion.Execution.Plan/Result`：实际完成的 Plan 与 `engine.Result`；
- `Completion.Run`：run index、seed、digest、Corpus outcome。

它是同步内存回调，`Execution` 标记 `json:"-"`，没有版本化 schema，也没有
artifact/replay/config reference。它足以作为 Stage 1 builder 的输入来源，但
不应直接成为跨版本持久接口。

最小缺口：一个新叶层 package，在 `OnRunComplete` 等“候选闭环已经结束”的
位置由调用方显式构建紧凑只读 Record；稳定内核无需 import 它。

## 10. 已识别风险

1. 若在 `engine.Engine.run` 内构建 Record，会造成 Engine 反向依赖 Assurance。
2. 多个作用域结果类型可能诱发重复 schema；Record 必须引用/摘要而非替换。
3. full artifact 对保留候选已足够，Record 不应再嵌入 Trace/State/Finding。
4. Trace/Observation 体积可能很大；完整中间 Observation 当前也未持久化。
5. `model.State.Text` 可能包含后端展示文本；稳定身份应使用有序 `State.Key`
   digest，不依赖 Text 或 map 展示顺序。
6. Replay 的 config + trace + ExecutionID/Seed 可复用，不应另造 replay plan。
7. 当前 configuration fingerprint 能覆盖 CLI/experiment/policy/settings，
   包含 profile、节点和 faults，但位于 CLI 层且是 opaque digest；Record 应接收
   它，不自行重算。
8. `minimize.Signature` 是当前稳定失败判据，但部分字段来自规范化错误文本。
9. snapshot payload 已被 `core.Message` 的 digest 设计排除，Record 不得重复写。
10. 跨协议 Record 不能把 Raft term/index/node 字段写进顶层公共 schema；协议
    细节留在既有 Trace/Model artifact。
11. CLI、Corpus、Persistence 的默认路径都不需要为 Stage 1 改动；若强行自动
    写 Record，会影响 feature-off artifact 行为。

## 11. 未确认项

- 没有启动实际 TLC 服务，因此未现场确认 `/health` 的运行版本和本机 server
  bounds；源码与脚本确认 jar 版本固定为 1.8.0，运行时 profile/bounds 仍必须
  由具体 config 与 health handshake 共同确认。
- 当前没有面向全部普通 experiment candidate 的完整持久化事实；是否需要为
  某一特定 Facet 增加中间 Observation evidence，须等 Facet 语义冻结后决定，
  不属于 Stage 1 Execution Record。
