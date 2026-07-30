# Raft Facet Catalog v1

## 1. Catalog 冻结

本 Catalog 恰好包含三个 Facet：

| facet_id | version | grounding | scope | 最大 class 数 |
|---|---:|---|---|---:|
| `raft.election_role_term_shape` | 1 | `implementation_grounded` | `state` | 13 |
| `raft.replication_alignment_shape` | 1 | `implementation_grounded` | `state` | 8 |
| `raft.snapshot_lifecycle_event` | 1 | `cross_layer` | `transition` | 10 |

三个 Facet只依赖 `core.NodeObservation` 或 `core.StepRecord.Effects`。它们不读取
Runtime/RawNode，不解析 `model.State.Text`，不需要新增 Trace 字段。

## 2. 通用 source 与 validation

state occurrence 按以下顺序产生：

1. 调用方显式提供的 `engine.Result.Initial`；
2. 如果未提供 Initial 且 Trace 非空，使用 `Trace.Steps[0].NodesBefore`；
3. 每个 `Trace.Steps[i].NodesAfter`。

若显式 Initial 与首个 NodesBefore 同时存在，Stage 3 必须验证两者节点投影一致，
只评价一次。transition occurrence 是每个完整 `StepRecord`。

共同结构校验：

- Trace version、step count、ExecutionID/seed 与 Record 摘要一致；
- `Trace.Validate()` 成功；
- NodeID 唯一、ID/Epoch/Status 合法；
- 相邻 step 的相关节点投影连续；
- semantic integer 接受内存整数或 JSON round-trip 后的精确非负整数，拒绝分数、
  负数、NaN/Inf 和溢出。

每个 Facet只校验其 required fields；一个不相关 optional field 的缺失不得使整个
candidate invalid。

## 3. `raft.election_role_term_shape` v1

### 3.1 Definition

- name：Election role/term population shape
- protocol：Raft
- grounding：`implementation_grounded`
- scope：`state`
- rationale：把绝对 node/term identity 折叠为 running population 的 leader、
  candidate 和 term 分裂形态，区分 election instability 与稳定 leader 形态。
- related property families：Election Safety、Leader Election、Term monotonicity
- theoretical cardinality：13
- validation status：Stage 2 preregistered；Stage 3 需 golden/metamorphic tests

### 3.2 Exact evidence bindings

对每个 `core.NodeObservation`：

- `Status`
- `Semantic["role"]`
- `Semantic["term"]`

规则：

- `Status==running` 时 role 必须是 `follower|candidate|leader`，term 必须是非负整数。
- `Status==crashed` 时 role 必须是 `crashed`；其 term 不参与 running term shape，
  但若存在必须是合法非负整数。
- 节点数量为零是 `invalid_evidence`。
- 缺 role/term 是 `insufficient_evidence`；字段存在但类型/role/status 关系错误是
  `invalid_evidence`。

### 3.3 Canonicalization

对 running nodes 计算：

```text
L = leader_count bucket: none | one | multiple
C = candidate_count bucket: none | some
T = distinct running term count: uniform | split
```

`uniform` 表示 running nodes 的 term 集合大小为 1，`split` 表示至少 2。
若 running node 数为 0，使用单独 class `no_running_nodes`。不排序或输出 NodeID，
不输出绝对 term。

### 3.4 Exact class table

除 `no_running_nodes` 外，class id 由下表三个冻结分量直接拼接。表中条件互斥且
穷尽所有至少一个 running node 的合法状态。

| class_id | leader 条件 | candidate 条件 | term 条件 |
|---|---|---|---|
| `leaders_none_candidates_none_terms_uniform` | 0 | 0 | distinct=1 |
| `leaders_none_candidates_none_terms_split` | 0 | 0 | distinct>=2 |
| `leaders_none_candidates_some_terms_uniform` | 0 | >=1 | distinct=1 |
| `leaders_none_candidates_some_terms_split` | 0 | >=1 | distinct>=2 |
| `leaders_one_candidates_none_terms_uniform` | 1 | 0 | distinct=1 |
| `leaders_one_candidates_none_terms_split` | 1 | 0 | distinct>=2 |
| `leaders_one_candidates_some_terms_uniform` | 1 | >=1 | distinct=1 |
| `leaders_one_candidates_some_terms_split` | 1 | >=1 | distinct>=2 |
| `leaders_multiple_candidates_none_terms_uniform` | >=2 | 0 | distinct=1 |
| `leaders_multiple_candidates_none_terms_split` | >=2 | 0 | distinct>=2 |
| `leaders_multiple_candidates_some_terms_uniform` | >=2 | >=1 | distinct=1 |
| `leaders_multiple_candidates_some_terms_split` | >=2 | >=1 | distinct>=2 |
| `no_running_nodes` | 无 running node | 不适用 | 不适用 |

`not_applicable`：无；任何合法非空 Raft node state 都在定义域中。

注意：multiple leaders 不是 Facet 自己宣告 Oracle violation。Oracle 仍由
`internal/oracle/raft.Checker` 判断；Facet只记录 population shape。

### 3.5 Invariances

| invariant | 结论 |
|---|---|
| Node renaming | 满足；只计数 |
| MessageID renaming | 满足；不读消息 |
| uniform term shift | 满足；只比较 term 相等关系 |
| uniform log/index shift | 满足；不读 index |
| Artifact layout | 满足 |
| ExecutionID/seed | 满足 |
| map iteration | 满足；按固定字段读取 |
| unrelated debug text | 满足 |

### 3.6 Examples

正例：

1. `{n1 leader t3, n2 follower t3, n3 follower t3}` →
   `leaders_one_candidates_none_terms_uniform`。
2. `{n1 candidate t4, n2 follower t3, n3 follower t3}` →
   `leaders_none_candidates_some_terms_split`。
3. `{n1 crashed, n2 leader t8, n3 candidate t9}` →
   `leaders_one_candidates_some_terms_split`。

边界例：

1. 单个 running follower、其余 crashed →
   `leaders_none_candidates_none_terms_uniform`；单节点仍是 uniform。
2. 全部 crashed → `no_running_nodes`，不读取 crashed nodes 的 term 来制造 split。

反例：

- “term 从 3 到 4 后最终选出 leader”需要跨 transition 历史，不是本 Facet
  class。两个状态分别独立分类。

### 3.7 与现有 coverage 的关系

`ProjectCoverage` 的 whole-state key 保留活动节点集合、按节点 role、term ranks、
log、commit、votes 等组合。本 Facet刻意丢弃 NodeID、vote target、log 和绝对
term，只保留一个 election population 维度。它可把很多 raw/semantic state
归为同一 class，也可直接比较不同 node renaming 下的相同 election shape。

预期区分的冗余类别：相同 TLC/implementation 行为阶段中稳定 leader、无 leader
竞选、split-term 竞选和多 leader population；不把不同 log payload 当作不同
election class。

## 4. `raft.replication_alignment_shape` v1

### 4.1 Definition

- name：Replication commit/applied/log alignment shape
- protocol：Raft
- grounding：`implementation_grounded`
- scope：`state`
- rationale：独立观察节点间 log tail、commit 和 applied 三个边界是否对齐，
  不保留绝对 index 或节点身份。
- related property families：Log Matching、State Machine Safety、Commit/Apply
  Progress
- theoretical cardinality：8
- validation status：Stage 2 preregistered

### 4.2 Exact evidence bindings

对全部 observed nodes（包括 crashed node）读取：

- `Semantic["last_index"]`
- `Semantic["commit"]`
- `Semantic["applied"]`

节点必须非空，三个字段必须是非负整数，并满足：

```text
applied <= commit <= last_index
```

缺字段是 `insufficient_evidence`；字段类型错误或关系违反是
`invalid_evidence`。crashed nodes 被纳入，因为 Adapter 从 hard state/storage
提供其持久边界；忽略它们会把 crash 前的 replication divergence 隐藏掉。

### 4.3 Canonicalization

分别计算：

```text
log_aligned     := 所有 last_index 相等
commit_aligned  := 所有 commit 相等
applied_aligned := 所有 applied 相等
```

每个布尔值只输出 `aligned|diverged`。单节点状态三个分量均为 aligned。
不输出绝对 index、最大 lag、NodeID 或 log digest。

### 4.4 Exact class table

三个布尔分量构成 8 个互斥且穷尽合法定义域的 class：

| class_id | last_index | commit | applied |
|---|---|---|---|
| `log_aligned_commit_aligned_applied_aligned` | 全相等 | 全相等 | 全相等 |
| `log_aligned_commit_aligned_applied_diverged` | 全相等 | 全相等 | 不全相等 |
| `log_aligned_commit_diverged_applied_aligned` | 全相等 | 不全相等 | 全相等 |
| `log_aligned_commit_diverged_applied_diverged` | 全相等 | 不全相等 | 不全相等 |
| `log_diverged_commit_aligned_applied_aligned` | 不全相等 | 全相等 | 全相等 |
| `log_diverged_commit_aligned_applied_diverged` | 不全相等 | 全相等 | 不全相等 |
| `log_diverged_commit_diverged_applied_aligned` | 不全相等 | 不全相等 | 全相等 |
| `log_diverged_commit_diverged_applied_diverged` | 不全相等 | 不全相等 | 不全相等 |

`not_applicable`：无；任何具备三个边界字段的非空 Raft state 都在定义域。

### 4.5 Invariances

| invariant | 结论 |
|---|---|
| Node renaming | 满足；使用全体相等关系 |
| MessageID renaming | 满足 |
| uniform term shift | 满足；不读 term |
| uniform log/index shift | 满足；对所有三个边界加同一常量不改变相等关系和合法关系 |
| Artifact layout | 满足 |
| ExecutionID/seed | 满足 |
| map iteration | 满足 |
| unrelated debug text | 满足 |

### 4.6 Examples

正例：

1. `(last,commit,applied)` 为
   `n1=(4,4,4), n2=(4,4,4), n3=(4,4,4)` →
   `log_aligned_commit_aligned_applied_aligned`。
2. `n1=(5,3,3), n2=(4,3,3), n3=(3,3,3)` →
   `log_diverged_commit_aligned_applied_aligned`。
3. `n1=(5,5,5), n2=(5,4,3), n3=(5,4,4)` →
   `log_aligned_commit_diverged_applied_diverged`。

边界例：

1. 单节点 `(0,0,0)` → 三者 aligned。
2. 一个 crashed node 保留 `(2,1,1)`，两个 running nodes 为 `(3,2,2)` →
   三者 diverged；crashed storage evidence 不被丢弃。

反例：

- `applied=5, commit=4` 是 `invalid_evidence`，不能归到 applied diverged。
- “follower 落后 17 条”为无界数值，不是 class；本 Facet只表达是否对齐。

### 4.7 与现有 coverage 的关系

现有 semantic state key 包含按节点 log shape、commit lag、replication rows 等更
细组合且保留节点位置。本 Facet是其正交的低基数摘要：绝对 index、entry value、
term 和 node permutation 改变时，只要三个跨节点 alignment 布尔值不变，class
不变。

预期区分的冗余执行类别：log tail 已同步但 commit/applied 未同步、commit 已同步
但 log tail 分叉、三层都对齐；不会把不同绝对高度重复计为新 class。

## 5. `raft.snapshot_lifecycle_event` v1

### 5.1 Definition

- name：Snapshot/storage lifecycle event
- protocol：Raft
- grounding：`cross_layer`
- scope：`transition`
- rationale：把 Adapter 已结构化记录、Mapper/metrics 已消费的 snapshot/storage
  lifecycle marker 映射为有限操作类别。
- related property families：Snapshot Installation、Log Compaction、Recovery、
  Snapshot Transport Status
- theoretical cardinality：10
- validation status：Stage 2 preregistered

cross-layer 的理由：source 是 implementation `core.ModelEvent`，同时这些 marker
在 storage-snapshot Mapper 中有明确 model action 对应。Facet不需要重新调用
Mapper。

### 5.2 Exact evidence bindings

遍历 `StepRecord.Effects`，只处理：

```text
Effect.Kind == core.EffectModelEvent
Effect.ModelEvent != nil
```

recognized name 与必需 schema：

| marker | 必需字段 |
|---|---|
| `raft.snapshot_created` | valid `Node`; `index>0`; integral `term>=0`; `snapshot_bytes>=0` |
| `raft.log_compacted` | 上述 snapshot boundary；`compact_index>0`; `compacted_entries>0` |
| `raft.snapshot_sent` | boundary；valid `to!=Node`; `pending_snapshot==index`; `next_index==index+1`; `progress_state=="StateSnapshot"`; integral `match_index` |
| `raft.snapshot_delivered` | valid Node；boundary |
| `raft.snapshot_applied` | valid Node；boundary |
| `raft.snapshot_fast_forwarded` | valid Node；boundary；若 before/after 存在则均为整数且 `commit_after>=commit_before` |
| `raft.snapshot_rejected_or_stale` | valid Node；boundary；optional reason 只作 explanation |
| `raft.snapshot_status_reported` | valid reporter Node；valid distinct `from/to` 且 `to==Node`；boolean `handled/reject` |

`boundary` 指 `index>0` 和 integral `term>=0`；`snapshot_bytes` 是 Adapter marker
固定字段。已知 marker 缺少必需字段为 `invalid_evidence`，不是
`not_applicable`。

### 5.3 Canonicalization 与 class table

每一个 recognized marker instance 产生一个 class observation。class 只依赖 name
以及 status marker 的两个 boolean，不包含 NodeID、index、term 或 MessageID。

| class_id | 精确判定 |
|---|---|
| `snapshot_created` | marker name 为 `raft.snapshot_created` |
| `log_compacted` | `raft.log_compacted` |
| `snapshot_sent` | `raft.snapshot_sent` |
| `snapshot_delivered` | `raft.snapshot_delivered` |
| `snapshot_applied` | `raft.snapshot_applied` |
| `snapshot_fast_forwarded` | `raft.snapshot_fast_forwarded` |
| `snapshot_rejected_or_stale` | `raft.snapshot_rejected_or_stale` |
| `snapshot_status_succeeded` | `raft.snapshot_status_reported` 且 `handled=true,reject=false` |
| `snapshot_status_failed` | `raft.snapshot_status_reported` 且 `handled=true,reject=true` |
| `snapshot_status_ignored` | `raft.snapshot_status_reported` 且 `handled=false`；此时 reject 不改变 class |

互斥性：

- 每个 marker instance 恰好属于一个 class。
- 单个 transition 可以包含多个 marker（例如 delivered + applied + status
  succeeded），因此 transition-level output 是 class set，不要求单值互斥。
- candidate union 对重复 key 去重并保留第一次 step/effect occurrence。

穷尽性：上表穷尽 recognized lifecycle marker。其他合法 Effect/ModelEvent 不在
本 Facet定义域，该 transition 若没有 recognized marker 则 `not_applicable`。

### 5.4 Invariances

| invariant | 结论 |
|---|---|
| Node renaming | 满足；只验证 endpoint 关系，不输出 ID |
| MessageID renaming | 满足；不读 MessageID |
| uniform term shift | 满足；term 只校验为合法，不进 class |
| uniform log/index shift | 满足；index 关系校验平移不变，值不进 class |
| Artifact layout | 满足 |
| ExecutionID/seed | 满足 |
| map iteration | 满足；按固定字段名读取，Effect 顺序仅决定 occurrence metadata |
| unrelated debug text | 满足；optional reason 不进 key |

### 5.5 Examples

正例：

1. transition effects 含 `raft.snapshot_created(index=2,term=1)` 和
   `raft.log_compacted(compact_index=2,compacted_entries=2)` → 两个 keys：
   `snapshot_created`、`log_compacted`。
2. MsgSnap delivery effects 含 `snapshot_delivered`、`snapshot_applied`、
   `snapshot_status_reported(handled=true,reject=false)` → 三个 keys：
   delivered、applied、status_succeeded。
3. Drop MsgSnap 后 status marker 为 `handled=true,reject=true` →
   `snapshot_status_failed`。

边界例：

1. status marker `handled=false,reject=true` 仍是 `snapshot_status_ignored`；
   ignored 优先于 reject。
2. 同一 transition 两次相同合法 marker 产生两个 observations，但 candidate
   coverage 只有一个 key，first occurrence 指向第一项。

反例：

- 普通 MsgApp transition 没有 recognized marker → `not_applicable`。
- 只有自然语言日志“snapshot installed”而无 structured Effect 不可评价。
- “send 失败、下一 heartbeat retry、最后 applied”需要多步序列，不属于本 Facet。

### 5.6 与现有 coverage 的关系

现有 semantic transition coverage hash 整个 projected before、model event name 和
after；同一 lifecycle operation 在不同 log/role 全状态中会形成不同 key。本 Facet
只回答本 transition 出现了哪一种 snapshot/storage lifecycle operation。

预期区分的冗余执行类别：create/compact/send/deliver/apply/fast-forward/stale 和
status outcome；不会因 snapshot index、sender ID 或整个模型状态不同而膨胀。

## 6. Deferred

| 项目 | 不进入 v1 的原因/缺失 evidence | 类别 | 重新评估条件 | 是否需改 Frozen Kernel |
|---|---|---|---|---|
| network queue topology | `StepRecord` 不保存每步完整 `Observation.Messages`；只有 Initial/Final queue | 基础设施/evidence | 有独立、只读且有界的逐步 queue evidence | 可能；本轮禁止 |
| exact timeout race | timer semantic 有部分值，但无冻结的跨节点/queue race snapshot | Facet/evidence | 冻结 typed timer evidence 和 race predicate | 可能 |
| remaining timeout/deadline | running node 暴露 ticks remaining，但 crashed/旧 artifact/profile 可缺，尚无独立有限 catalog | Facet | 有明确 profile/version binding 和 golden data | 不一定 |
| multi-step snapshot retry | 必须匹配 send/status/heartbeat/resend/install 序列 | Goal/Waypoint | Goal core 完成后 | 否，现有 marker 可能足够 |
| multi-step crash/restart recovery path | status/epoch 存在，但 recovery 是历史进度而非单 state/transition | Goal/Waypoint | Goal core 完成后 | 否 |
| cross-facet combination | v1 明确不做 Cartesian product | Facet coverage | 单独预注册 pairwise 价值和上限 | 否 |
| Facet-based Corpus admission | Stage 3 offline-only，尚无 novelty/admission evidence | 搜索/基础设施 | offline evaluator 验证后另立阶段 | 可能需窄 adapter，不能反向依赖 |
| Agent-generated Facet | 尚无 proposal validator，不能让自然语言成为 key | Agent | deterministic proposal schema/validator 完成 | 否 |
| implementation-model divergence Facet | `model.State.Text` 无公开 typed variable projection，Event/Step 对齐也非 1:1 | cross-layer/evidence | 提供冻结 typed model projection 与 alignment | 可能，不能解析 Text 绕过 |
| Artifact loader | Stage 3 调用方显式提供 evidence | 基础设施 | evaluator 稳定后另建只读 loader | 否 |
| offline historical campaign | 本轮禁止实验，且旧 policy 可能只有 summary | 实验 | loader/availability audit 完成 | 否 |
| lifecycle/recovery state Facet | v1 已有三个维度；避免把 Catalog 扩大为全集 | Facet | 三个 v1 Facet验证后评估新增价值 | 否 |

snapshot/storage 未标为 `DEFERRED_EVIDENCE_GAP`，因为当前 Trace 已有完整、结构化
的 lifecycle marker，足以实现本 Catalog 的 transition Facet。只有多步 retry 被
defer。
