# Stage 4 真实 etcd-raft 短轨迹 Facet Pilot 报告

## 1. 结论

Stage 4 Pilot 为 **GO**。

在 commit `75d4e51120b370acb880d003629f916da3f1a080`、分支
`agent/semantic-assurance-rebuild-v1` 上，测试执行了 8 个固定场景，每个场景用
相同 Plan、seed 和配置重复 3 次，共 24 个真实 candidate。执行链为：

```text
fixed Plan / deterministic ActionSource
  -> runtime.Runtime
  -> adapters/etcdraft.Adapter
  -> engine.Engine
  -> experiment.Runner.RunFeedback
  -> experiment.Completion
  -> executionrecord.BuildV1
  -> facet.EvaluateAll(..., raft.CatalogV1())
```

三个 Facet 均达到预注册非退化门槛；24 次 Election 和 Replication evaluation
全部为 `evaluated`，12 次 snapshot 场景为 `evaluated`，12 次无 snapshot marker
场景为 `not_applicable`。没有 `insufficient_evidence` 或 `invalid_evidence`。

本结果只证明三个冻结 Facet 在这组真实短轨迹上可组合、可重复且不退化，不是正式
coverage 统计实验，也不证明 Facet 优于现有 coverage 或任何搜索策略。

## 2. 环境与冻结配置

| 项 | 值 |
|---|---|
| HEAD | `75d4e51120b370acb880d003629f916da3f1a080` |
| Branch | `agent/semantic-assurance-rebuild-v1` |
| Go | `go1.26.4 linux/amd64` |
| SUT | 本地、进程内 `go.etcd.io/raft/v3` replace |
| 节点 | `n1,n2,n3` |
| Mapper profile | `storage-snapshot` |
| `MaxValue` | 5 |
| `MaxLogIndex` | 10 |
| `LargestTerm` | 10 |
| `EmitLeaderNoOp` | true |
| Fault policy | 默认正确实现；无 mutant |
| PreVote / CheckQuorum | false / false（Adapter 冻结默认） |
| Parallelism | 1 |
| Artifact / replay | 均未调用 |
| TLC | 未启动；使用一次调用的确定性 test-only `model.Executor` |

普通场景也使用 `storage-snapshot` Mapper；Adapter snapshot threshold 为 3、retain
为 1。`SnapshotFastForward` 按其已有合法配置使用 threshold 4、retain 1。
Runtime 上限仅作为短测试安全边界；每个在线 policy 均主动结束，没有 budget
exhaustion。

## 3. 已审计并复用的真实场景资产

| 路径 | helper/type/function | 已验证行为 | Stage 4 用法 |
|---|---|---|---|
| `examples/plans/election.json` | 固定 `PlanSequence` | timeout、vote、leader election | 原动作序列用于 A |
| `examples/plans/client-request-commit.json` | 固定 `PlanSequence` | leader no-op、request、replication/commit | 原动作序列用于 C |
| `examples/plans/follower-crash-restart.json` | 固定 `PlanSequence` | follower crash/restart/catch-up | 原动作序列用于 D |
| `internal/adapters/etcdraft/adapter_test.go` | election helper tests | 受控 timeout/message delivery | B 复用同一公开动作语义 |
| 同上 `TestLaggingFollowerReceivesSnapshotThroughRuntimeAndRejectsDuplicate` | 真实 Runtime/Adapter 场景 | sent/applied/stale/status | 由已有公开 policy 覆盖 |
| 同上 `TestFollowerNaturallyFastForwardsMatchingSnapshot` | 真实 Runtime/Adapter 场景 | matching snapshot fast-forward | 由 `SnapshotFastForward` 覆盖 |
| 同上 `TestLaggingFollowerRejectsOlderSnapshotAfterNewerSnapshotAndRestarts` | 真实 Runtime/Adapter 场景 | stale/newer snapshot 与 restart | 审计参考；未复制私有 helper |
| `internal/policy/snapshot_partition.go` | `SnapshotPartition` | partition、snapshot catch-up、duplicate、first failure | E、F、G |
| `internal/policy/snapshot_fast_forward.go` | `SnapshotFastForward` | stale rejection 后自然 fast-forward | G |
| `internal/experiment/runner.go` | `Runner.RunFeedback`、`Hooks.OnRunComplete` | 真实 run/corpus/digest 完成边界 | 所有 candidate |

测试没有复制 `adapter_test.go` 的私有 harness；它直接用公开
`etcdraft.New`、`runtime.New`、`engine.New` 和公开 policy。在线 policy 外包一层
test-only recording `ActionSource`，只记录它实际返回的 `plan.PlanAction`；记录结果
作为 `FeedbackExecution.Plan`，没有把 Concrete Action 冒充高层 Plan。

## 4. 场景与真实执行结果

下表每行是三次重复中的第一份；后两份的稳定语义摘要完全相同。

| Scenario | Family / source | Seed | Plan / concrete / steps | Effects / model events | PlanDigest | TraceDigest |
|---|---|---:|---:|---:|---|---|
| `election_stabilization` | A / `examples/plans/election.json` | 4401 | 3 / 3 / 3 | 8 / 5 | `bd7592c0…e7c5` | `fb9947da…98a8` |
| `election_contention` | B / Adapter election semantics | 4402 | 4 / 4 / 4 | 11 / 6 | `a33a19c6…6391` | `c6f97f15…d3d9` |
| `replication_lag_catchup` | C / `client-request-commit.json` | 4403 | 9 / 9 / 9 | 22 / 17 | `12b007dd…c76` | `aa92675d…1db6` |
| `crash_restart_recovery` | D / `follower-crash-restart.json` | 4404 | 10 / 10 / 10 | 19 / 15 | `078955d2…9468` | `ab2f1e82…1fa1` |
| `snapshot_catchup_success` | E / `SnapshotPartition` | 4405 | 25 / 25 / 25 | 52 / 34 | `fefb83f2…7462f` | `378ba7bd…1af7` |
| `snapshot_failure_retry` | F / `SnapshotPartition(FailFirstSnapshot)` | 4406 | 29 / 29 / 29 | 61 / 37 | `80620663…e128` | `45753cdf…e348` |
| `snapshot_duplicate_stale` | G / `SnapshotPartition(DuplicateSnapshot)` | 4407 | 28 / 28 / 28 | 58 / 36 | `5f3d58bf…8b83` | `c9ba7e1a…0ece` |
| `snapshot_fast_forward` | G / `SnapshotFastForward` | 4408 | 31 / 31 / 31 | 72 / 47 | `02e0b245…aa8b` | `827e34a6…e98a` |

所有 run 的 Engine/Experiment status 均为 `completed`，Corpus admission 均为
`retained_raw`。`facet.EvaluateAll` 前后对 `engine.Result` 的 JSON 语义、
`experiment.Run`（含 Corpus outcome）和 model executor 调用数逐一比较，均不变。
每个 candidate 的 fake model executor 恰好调用一次；mutator 调用数为 0。

总计：24 candidates、417 个 PlanAction、417 个 Concrete Action、417 个 Trace
step、909 个 Effect、591 个 model event。没有 run artifact 目录。

## 5. 每个场景的 Facet key 与 first occurrence

以下使用 `class_id@occurrence`；其完整 canonical key 统一为：

```text
modelfuzz-ng-facet-key-v1/<facet_id>/v1/<scope>/<class_id>
```

### `election_stabilization`

- Election：`leaders_none_candidates_none_terms_uniform@initial`；
  `leaders_none_candidates_some_terms_split@step0`；
  `leaders_one_candidates_none_terms_split@step2`。
- Replication：`log_aligned_commit_aligned_applied_aligned@initial`；
  `log_diverged_commit_aligned_applied_aligned@step2`。
- Snapshot：`not_applicable`。

### `election_contention`

- Election：initial uniform/no leader；`leaders_none_candidates_some_terms_split@step0`；
  `leaders_none_candidates_some_terms_uniform@step2`；
  `leaders_one_candidates_some_terms_uniform@step3`。
- Replication：fully aligned at initial；
  `log_diverged_commit_aligned_applied_aligned@step3`。
- Snapshot：`not_applicable`。

### `replication_lag_catchup`

- Election：与稳定选举相同的三个 key，leader key 首见于 step 2。
- Replication：fully aligned at initial；
  `log_diverged_commit_aligned_applied_aligned@step2`；
  `log_diverged_commit_diverged_applied_diverged@step5`。
- Snapshot：`not_applicable`。

### `crash_restart_recovery`

- Election：initial、candidate split(step 1)、candidate uniform(step 2)、
  `leaders_one_candidates_none_terms_uniform@step3`、
  `leaders_one_candidates_none_terms_split@step4`。
- Replication：fully aligned at initial；log-only divergence(step 3)；
  all-diverged(step 8)。
- Snapshot：`not_applicable`。

### `snapshot_catchup_success`

- Election：initial、candidate split(step 0)、candidate uniform(step 2)、
  one leader/uniform(step 3)。
- Replication：fully aligned(initial)、log-only divergence(step 3)、
  all-diverged(step 6)、log-aligned/commit-applied-diverged(step 23)。
- Snapshot：`snapshot_created@step16/effect2`；
  `log_compacted@16/3`；`snapshot_sent@22/2`；
  `snapshot_delivered@23/1`；`snapshot_applied@23/2`；
  `snapshot_status_succeeded@23/4`。

### `snapshot_failure_retry`

与 success 的 state Facet 形状一致；Snapshot 额外包含
`snapshot_status_failed@step23/effect0`，重试成功后在 step 27 首次出现 delivered、
applied、succeeded。

### `snapshot_duplicate_stale`

与 success 的 state Facet 形状一致；Snapshot 在 step 24 首次 applied/succeeded，
在 step 25 出现
`snapshot_rejected_or_stale@effect2` 与
`snapshot_status_ignored@effect4`。

### `snapshot_fast_forward`

Election 与 snapshot 场景相同。Replication 包含 fully aligned、log-only
diverged、all-diverged、log-aligned/commit-applied-diverged。Snapshot 为
`snapshot_created@24/2`、`log_compacted@24/3`、`snapshot_sent@28/2`、
`snapshot_delivered@29/1`、`snapshot_fast_forwarded@29/2`、
`snapshot_status_succeeded@29/9`。

所有 snapshot occurrence 都由测试反查同一真实 `core.Trace` 的对应
`StepRecord.Effects[effect_index]`，要求其为 Adapter 产生的
`core.EffectModelEvent`，没有注入 marker。

## 6. Observed class 与 candidate-presence frequency

频率按 24 个 candidate 内去重后的 key presence 统计，不是 raw occurrence。

### Election（6/13）

| Class | Candidate presence |
|---|---:|
| `leaders_none_candidates_none_terms_uniform` | 24 |
| `leaders_none_candidates_some_terms_split` | 24 |
| `leaders_none_candidates_some_terms_uniform` | 18 |
| `leaders_one_candidates_none_terms_split` | 9 |
| `leaders_one_candidates_none_terms_uniform` | 15 |
| `leaders_one_candidates_some_terms_uniform` | 3 |

未观察到的 7 类：
`leaders_multiple_candidates_none_terms_split`、
`leaders_multiple_candidates_none_terms_uniform`、
`leaders_multiple_candidates_some_terms_split`、
`leaders_multiple_candidates_some_terms_uniform`、
`leaders_none_candidates_none_terms_split`、
`leaders_one_candidates_some_terms_split`、`no_running_nodes`。
multiple-leader 类属于 `EXPECTED_ONLY_UNDER_FAULT_OR_BUG`；其余为
`NOT_OBSERVED_IN_PILOT`，不能据此宣称不可达。

### Replication（4/8）

| Class | Candidate presence |
|---|---:|
| `log_aligned_commit_aligned_applied_aligned` | 24 |
| `log_diverged_commit_aligned_applied_aligned` | 24 |
| `log_diverged_commit_diverged_applied_diverged` | 18 |
| `log_aligned_commit_diverged_applied_diverged` | 12 |

其余 4 类为 `NOT_OBSERVED_IN_PILOT`，需要单独定向场景或错误实现：
`log_aligned_commit_aligned_applied_diverged`、
`log_aligned_commit_diverged_applied_aligned`、
`log_diverged_commit_aligned_applied_diverged`、
`log_diverged_commit_diverged_applied_aligned`。

### Snapshot（10/10）

| Class | Candidate presence |
|---|---:|
| `snapshot_created` | 12 |
| `log_compacted` | 12 |
| `snapshot_sent` | 12 |
| `snapshot_delivered` | 12 |
| `snapshot_applied` | 9 |
| `snapshot_fast_forwarded` | 3 |
| `snapshot_rejected_or_stale` | 3 |
| `snapshot_status_succeeded` | 12 |
| `snapshot_status_failed` | 3 |
| `snapshot_status_ignored` | 3 |

Snapshot Catalog 没有未观察类。这里的 10/10 只说明已有短场景资产碰巧覆盖整个
transition class table，不是可达性证明或统计结论。

## 7. Key digest 核查

测试通过 Stage 3 `KeyV1.Digest()` 产生并验证完整 SHA-256。观察到的 representative
digest 包括：

- Election：initial `fa3d7023…9f04`；candidate split
  `1b0d206c…eb36`；one-leader uniform `4575e8f7…8a32`。
- Replication：fully aligned `08aa38ca…e440`；log-only diverged
  `46b8f019…edce`；all-diverged `9e7771f0…67e6`。
- Snapshot：created `f64c5e18…e5c2`；compacted `358d8caf…c5c`；
  sent `f515248b…4b6`；applied `c426ef4c…acaf`；
  fast-forwarded `e98a40dd…7682`；status failed `880ef559…758c`。

完整 canonical string 和 digest 由 `TestRealTraceFacetPilot -v` 的确定性 typed
summary 输出；报告不创建第二份持久化 schema。

## 8. 非退化门槛

| 门槛 | 结果 | 证据 |
|---|---|---|
| Election ≥3，含 none/candidate 与 one leader | PASS | 6 类 |
| Replication ≥2，含 fully aligned 与 diverged | PASS | 4 类 |
| Snapshot ≥5，含 created/compacted/sent/delivery/status | PASS | 10 类 |
| 跨 Facet 正交性 | PASS | `election_stabilization` step0 与 `election_contention` step2 的 replication 均 fully aligned，但 Election 分别为 candidate/split 与 candidate/uniform |
| 不同 TraceDigest 共享 FacetKey | PASS | `fb9947da…98a8` 与 `c6f97f15…d3d9` 均含 initial Election key |

正交性检查直接读取真实 `StepRecord.NodesAfter`，用 Stage 2 冻结的有限关系投影计算
test-only shape；不修改或替代生产 evaluator。

## 9. 重复性

每个场景重复 3 次。测试逐字段比较：

- Engine status；
- PlanDigest、TraceDigest、RecordDigest；
- Plan/Concrete action、effect、step、model-event count；
- 三个 Facet status；
- canonical key set、key digest；
- first occurrence；
- Corpus admission/retained。

`-count=1`、外层 `-count=3` 和 race Pilot 均通过。排序只用于 canonical output；
每个 repetition 在排序前已经具有相同 TraceDigest 和 occurrence，因此没有用排序
掩盖执行差异。

## 10. 边界与限制

- 未启动 strict TLC HTTP server；test-only executor 只证明 Engine 的 model
  completion 边界和 Corpus 判定位置可组合，不证明 TLC conformance。
- 未执行 replay；`BuildV1` 没有 ArtifactReference，因此 `Replayable=false`。
- 未写 Artifact、未调用 CLI、未接入 Corpus admission 策略。
- 所有 candidate 都是 initial candidate；mutator worker 从未被调用。
- B 场景是基于 Adapter 已验证公开动作语义的小型固定 Plan，不是新增生产 policy。
- 24 candidates 的频率只用于 sanity 检查，不能用于显著性、泛化或方法优劣结论。

基于以上证据，可以冻结独立的 Facet Breadth v1 Core 契约并进入 Stage 5；Stage 5
仍不得接入 Runner、Corpus、mutation 或 artifact。
