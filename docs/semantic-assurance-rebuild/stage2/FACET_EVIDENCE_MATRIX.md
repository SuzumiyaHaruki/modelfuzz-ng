# Facet Evidence Matrix

## 1. 审计结论

当前完成边界足以离线实现三个 v1 Facet，但证据分为两层：

- `CompletedExecutionRecordV1`：身份、count、digest、outcome 和 artifact 引用；
- 调用方显式提供的 typed evidence：`core.Trace`、可选初始
  `core.Observation`、`[]model.Event`、`[]model.State`、`[]oracle.Finding`。

Record 本身不包含可直接分类的节点状态。`Replayable=true` 也不保证调用方已经
加载 Trace。Facet evaluator 必须逐 definition 检查 required evidence。

## 2. Artifact policy 语义

`cmd/modelfuzz-ng/experiment_store.go` 定义：

- `all`：所有 run 写完整 run directory；
- `retained`：`Run.Retained || !Run.Succeeded` 才写完整目录；
- `failures`：仅 `!Run.Succeeded`；
- `summary`：不写逐 run 完整目录。

所有 policy 都向根级 `runs.jsonl` 追加 `experiment.Run`。完整 run directory 由
`writeCompletion`/`writeArtifacts` 写：

`config.json`、`plan.json`、`resolutions.json`、`actions.json`、`trace.json`、
`model-events.json`、`model-states.json`、`oracle-findings.json`、
`failure.json`、`result.json`、`candidate.json`、`run-summary.json`。

因此 summary-only 或未被 policy 选中的成功 run 通常只有紧凑 `Run`，不足以离线
计算本 Catalog 的 Facet。

## 3. 证据总表

“内存”指 `experiment.Completion.Execution.Result` 或其相邻完成对象；“完整
artifact”指上述 run directory。

| 来源 | 实际类型/字段 | 内存 | 完整 artifact | summary | retained/failures | 顺序/identity | Facet 用途 |
|---|---|---:|---:|---:|---:|---|---|
| Completed Record | `executionrecord.CompletedExecutionRecordV1` | 构建后有 | 目前不会自动写 | 不适用 | 不适用 | `RecordDigest`；artifacts canonical sorted | identity、count、evidence availability；不能单独分类 |
| Completion | `experiment.Completion{Run,Candidate,Execution}` | 有 | `Execution` 标记 `json:"-"`，不作为一个整体写 | `Run` 有 | hook 写分项 | Corpus 判定完成后产生 | Stage 3 programmatic caller 的完成视图 |
| Trace | `engine.Result.Trace core.Trace` | 有 | `trace.json`、也嵌在 `result.json` | 无 | policy 命中时有 | Version/ExecutionID/Seed；steps 按 index | 三个 v1 Facet的主要 evidence |
| Step | `core.StepRecord` | Trace 内 | trace/result 内 | 无 | policy 命中时有 | contiguous zero-based `Index` | transition 与 post-state |
| NodesBefore/After | `[]core.NodeObservation` | 每个完整 step 有 | trace/result 内 | 无 | policy 命中时有 | Validate 唯一 NodeID；原 slice 通常按 ID，但 Facet不得依赖 ID | election/replication state |
| Concrete Action | `StepRecord.Action core.Action` | 有 | trace/actions/result | action count only | policy 命中时有 | typed `ActionKind`，message actions 有 ID/selector | transition domain/debug；key 排除 IDs |
| Effects | `StepRecord.Effects []core.Effect` | 有 | trace/result | effect count/metrics only | policy 命中时有 | slice 顺序、逻辑时间非递减 | snapshot lifecycle transition |
| Model Event | `engine.Result.ModelEvents []model.Event` | 有 | model-events/result | count/name metrics | policy 命中时有 | flatten 后顺序稳定；无 step index | 对照；v1 Catalog 不依赖 event-to-step 绑定 |
| Model State | `engine.Result.ModelStates []model.State` | 有 | model-states/result | count、StateKeys | policy 命中时有 | slice 顺序；`Key` opaque；path digest | raw/semantic coverage 对照，不作为 v1 Facet source |
| Initial/Final | `engine.Result.Initial/Final core.Observation` | 有 | result.json | 无 | policy 命中时有 | `Observation.Normalized` 可稳定排序 | initial state；final queue/debug |
| Oracle | `[]oracle.Finding` | 有 | oracle-findings/result | count/codes/metrics | policy 命中时有 | Finding Step 是 1-based action；Record codes sorted dedup | outcome/context，不参与 class |
| failure | `*core.FailureRecord` | 失败时有 | failure/result | run error/status only | 失败 policy 有 | kind/operation/time；失败 Action 不入 Trace | 合法 prefix 的停止边界，非 transition |
| run summary | `experiment.Run` | 有 | runs.jsonl/run-summary | 有 | 有 | run index/seed/digests | count 和 consistency，不含 Facet evidence |
| Corpus | `corpus.Entry` | retained 时有 | corpus.jsonl | retained entry 有 | retained entry 有 | Plan + new keys；不含 Trace | 不作为 Facet 输入 |
| Runtime step view | `runtime.StepResult.Observation` 和 `BeforeObservation` | Engine 执行时有 | 每步完整 Observation 不写 | 无 | 无 | 当前时刻完整 queue | 在线 Plan/Mapper/Oracle；offline Facet不得要求 |
| Runtime/RawNode | `runtime.Runtime`、etcd `RawNode` | 仅执行中 | 不写 | 无 | 无 | mutable | 禁止 Facet读取 |

## 4. 结构化字段审计

### 4.1 NodeObservation

`internal/core/observation.go`：

```text
NodeObservation {
  ID, Epoch, Status, Digest, Semantic map[string]any
}
```

`internal/adapters/etcdraft/observation.go: Adapter.observation` 对 running node
稳定提供：

- `role`
- `term`
- `vote`
- `leader`
- `commit`
- `applied`
- `last_index`
- `last_term`
- `log_digest`
- election/heartbeat elapsed、timeout、ticks remaining

crashed node 提供 `role="crashed"`、hard-state term/vote/commit，以及保存的
applied/last-index/last-term/log-digest。`NodeObservation.Epoch` 在 restart 后递增。

storage-snapshot profile 启用时，`addSnapshotObservation` 还提供：

- `first_index`
- `snapshot_index`
- `snapshot_term`
- `snapshots_created`
- `snapshots_applied`
- `logs_compacted`
- `compacted_entries`

leader 额外有 `leader_progress[peer]`：

- `match`
- `next`
- `pending_snapshot`
- `state`

这些值在内存通常是 Go integer；经 `map[string]any` JSON round trip 后 number
通常为 `float64`。Stage 3 必须有一个窄的、无损整数读取器并拒绝负数、分数和
溢出，不能依赖具体动态 numeric type。

### 4.2 Message queue

`core.Observation.Messages []MessageObservation` 含：

- MessageID、from/to、SenderEpoch、link sequence、parent ID、position；
- enqueued logical time、TypeHint、PayloadDigest、Metadata、Blocked。

`Runtime.collectObservation` 把确定性 network 和 partition 注入完整 Observation。
但是 `core.StepRecord` 明确只保存节点快照；每一步完整 pending queue 不写 Trace。
只有 `Result.Initial.Messages` 和 `Result.Final.Messages` 在 `result.json` 中完整
保留。因而：

- 不能从离线 Trace 重建每一步 queue topology；
- `MessageID` 不是 Facet identity；
- `Blocked` 能区分当前完整 Observation 中的 partition-blocked message，但普通
  StepRecord 不包含这份 queue；
- network queue topology v1 deferred。

### 4.3 Timer

running node Semantic 有 elapsed/timeout/ticks-remaining。自然或强制 timer fire
由 `EffectTimerFired` 保存：

- Node/Epoch
- `TimerFireSource`
- TypeHint/RoleHint
- string Metadata；election timeout 可含 term-before/term-after

但每一步剩余 deadline 只在 NodesBefore/After 的 implementation semantic map
中，具体 timeout-race 还会涉及多个节点与 queue timing。v1 不选择 timeout Facet。

### 4.4 Snapshot/storage

snapshot lifecycle 不需要 RawNode。`core.EffectModelEvent.ModelEvent` 在
`StepRecord.Effects` 中保存以下 structured marker：

- `raft.snapshot_created`
- `raft.log_compacted`
- `raft.snapshot_sent`
- `raft.snapshot_delivered`
- `raft.snapshot_applied`
- `raft.snapshot_fast_forwarded`
- `raft.snapshot_rejected_or_stale`
- `raft.snapshot_status_reported`

事件由 `internal/adapters/etcdraft/snapshot.go`、`ready.go` 和 `adapter.go`
产生。`metrics.Collect` 已按同一名称统计，并把 status reported 分成 succeeded、
failed、ignored。Mapper 在 storage-snapshot profile 中将相应 marker 映射为
`CreateSnapshot`、`CompactLog`、`SendSnapshot`、`InstallSnapshot`、
`FastForwardSnapshot`、`RejectSnapshot`、`HandleSnapshotStatus`。

因此单 transition 的 snapshot lifecycle class 有充分、稳定的 Trace evidence。
多步 retry path 仍然需要历史状态机，属于 Goal。

## 5. 模型证据

`model.Event` 是 typed name + `map[string]any` params + Reset。Mapper 对每个
Concrete Transition 可输出 0..N events；`engine.Result.ModelEvents` 把它们追加成
一个扁平 slice。没有稳定的 per-step offset，故不能把第 k 个 Event 武断绑定到
第 k 个 Step。

`model.State` 只有：

```text
Text string
Key  int64
```

`internal/model/raft/coverage.go` 存在经过测试的私有 text projector，但：

- parser 是 package-private；
- public `ProjectCoverage` 只返回 hashed `int64` keys；
- Stage 3 若复制 parser 会形成第二套 TLA display-text 语义；
- `State.Key` 只是同后端/配置下 opaque stable key。

结论：v1 Facet不读取 `model.State.Text`，不把 `State.Key` 转成 class。Model
events/states 仅用于 evidence consistency、对照和未来 typed model projection。

## 6. 稳定顺序与摘要

- `Observation.Normalized` 按 NodeID/MessageID 排序，但 Facet key 不能含这些
  identity；node-renaming 通过计数和关系投影实现。
- `Trace.Steps` 有 contiguous index，`Effects` 按逻辑时间非递减。
- `experiment.digestTrace` 排除 ExecutionID/Seed/Metadata，但它是 package-private；
  Stage 3 不复制算法，只验证 Record 的 digest 格式与 typed evidence 的结构/count。
- `ModelStatePathDigest` 只覆盖有序 `State.Key` path，不提供 Facet 语义。
- `ObservationDigest` 可用于 replay mismatch 检查，不提供可反演状态。
- Node `Digest` 覆盖 Adapter semantic map，可作一致性 debug，不能替代字段读取。

## 7. 缺失与错误处理矩阵

| 情况 | Facet status |
|---|---|
| Record 无 trace artifact，调用方也无内存 Trace | `insufficient_evidence` |
| `Replayable=false`，但调用方有合法内存 Trace | 可正常评价；Replayable 不做 gate |
| Trace version 不受支持、step count 与 Record 不同 | `invalid_evidence` |
| 已提供合法 empty Trace 且无 Initial Observation | state Facet `insufficient_evidence`；snapshot transition Facet `not_applicable` |
| step 无 snapshot marker | snapshot transition `not_applicable` |
| marker 名称已知但必需 param 缺失/类型非法 | `invalid_evidence` |
| partial/failure result 有合法 Trace prefix | 评价 prefix |
| failure Action 只有 ObservationBefore | 不构造伪 transition |
| model state artifact 缺失 | 本 Catalog 不受影响；model-grounded future Facet不足 |
| summary-only run | 通常 `insufficient_evidence`；不得启动 Runtime 补齐 |

## 8. 不进入 v1 的证据

| 证据需求 | 当前结论 |
|---|---|
| 每一步完整 pending queue | Trace 未保存；只有运行时 StepResult 和 Initial/Final |
| 精确 timer deadline/race | 部分 semantic field 存在，但跨节点/queue race evidence 不完整 |
| partition 中每步 blocked topology | Action/partition transition 有，完整逐步 queue 无 |
| 多步 snapshot retry | marker 有，但需要 history/Goal |
| 多步 crash/restart recovery | status/epoch 有，但 path predicate 是 Goal |
| model typed variable view | 只有私有 text projector 和 opaque State.Key |
| RawNode progress beyond exposed leader_progress | mutable 且禁止读取 |
