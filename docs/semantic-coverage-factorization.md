# Raft 语义覆盖分解与状态增长归因

> 完整状态：`raft-coverage-v2-prototype`
> 新增切面：`raft-coverage-facets-v1-prototype`
> 状态：实验性离线分析；不替换 v1/v2，不参与 Corpus 准入。

## 1. 本轮目标和边界

第一轮证明 v2 比 v1 更粗，但 24 条随机轨迹仍然 24/24 产生新 v2 状态。本轮没有继续凭
直觉删字段，而是完成两件事：

1. 统计 v2 每个字段的基数，并用单字段、字段组消融定位状态增长来源；
2. 把 Election、Replication、Snapshot、Recovery、Network 分成五个独立 Coverage
   Facet，避免再次构造五者的完整笛卡尔积。

本轮已实现的是显式调用的只读分析能力。没有修改 v1、v2 的序列化，没有修改
Corpus、Mutation、Replay、TLC、Oracle、Runtime、Raft/Snapshot 行为，也没有实现
coverage stall、Behavior Goal、Waypoint 或 LLM 在线调度。

## 2. v2 字段、来源和依赖

v2 的直接输入仍然只是完整 TLC model state。下表中的“派生”表示投影时从 TLC 变量
计算，不表示 TLC state 原本就有同名变量。

| v2 字段 | 来源 | 归一化 | 主要依赖或重复关系 |
|---|---|---|---|
| `cluster_size` | TLC tuple 长度 | 保留节点数 | 本数据固定为 3 |
| `quorum_available` | `currentActive` | 布尔值 | 可由 node shape 的 lifecycle 计数推导 |
| `role_topology` | `currentActive`、`state` | role/lifecycle 计数 | 可由 canonical node shapes 推导 |
| `term_topology` | `currentTerm`、active set | 相对 term 类别 | 与 node `term_position` 高度相关，但不完全等价 |
| `leader_term_position` | role、term | 相对最大 term | 可由 node role/term position 大体推导 |
| `candidate_term_position` | role、term | 相对最大 term | 可由 node role/term position 大体推导 |
| `log_topology` | `log`、`commitIndex` | equal/prefix/conflict | 与 node `log_relation` 高度相关 |
| `committed_prefixes` | `log`、`commitIndex` | agree/conflict | 是安全性重要摘要；本批数据始终为 agree |
| `catch_up_topology` | log、leader progress、storage | bucket 多重集 | 与 node `inbound_catch_ups`、leader lag 重复 |
| `snapshot_topology` | storage/snapshot 变量 | phase 多重集 | 与 node `snapshot_phase` 重复 |
| `voting_topology` | `votesGranted`、`votedFor` | 投票关系多重集 | 与 node `candidate_votes`/`voted_for` 重复 |
| `canonical_node_shapes` | 上述全部 TLC 事实 | 去 ID、排序、计数 | 包含 lifecycle、role、term、log、lag、snapshot、vote、catch-up |

canonical node shape 的 11 个子字段为：

`lifecycle`、`role`、`term_position`、`log_relation`、`commit_lag`、
`applied_lag`、`snapshot_phase`、`voted_for`、`candidate_votes`、
`leader_peer_lags`、`inbound_catch_ups`。

所有值都已去除节点 ID、绝对 term/index 和完整 entry 序列。lag 使用
`zero/one/small/large` bucket。当前问题不是原始数值泄漏，而是多个合理维度在 node
shape 内组合，同时又被顶层字段重复表达。

## 3. 分析命令和输出

```bash
go run ./cmd/modelfuzz-ng coverage-factorize \
  -input /path/to/experiment-or-run \
  -output /tmp/raft-factorization.json
```

输入可以是实验目录、单个 run artifact 目录或 `model-states.json`。每个 run 必须同时
具有 `model-states.json`、`model-events.json`、`trace.json`、`config.json`，以及含
`initial_observation` 的 `result.json`。`candidate.json` 存在时用于读取可靠的候选来源；
缺少时只标为 `unknown`，不会猜测策略。

工具递归扫描并按路径排序执行，拒绝把输出写入输入目录。原来的 `coverage-compare`
行为保持不变。JSON 报告包含完整 v1/v2 对比、字段基数、全部单字段消融、8 个字段组
消融、Top-5 条件分裂、五个 Facet、四个 interaction，以及可可靠恢复的场景分组。
报告保留可读结构和 run/state 示例，不只输出 hash。

消融指标：

```text
Contribution(X) = 1 - distinct_without_X / distinct_full
```

它只表示字段对当前样本状态基数的贡献，不表示字段没有测试价值。一个安全性关键字段
在正常样本中可能恒定，贡献为 0，但发生故障时仍可能是最重要的区分。

## 4. CoverageFrame：model state 与 Trace 的确定性对齐

离线分析为每个 run 构造 `CoverageFrame`，包含 run/source、trace step、model event、
model state、触发 Action、Effects 和派生 Network/Recovery/Snapshot 上下文。

对齐规则如下：

1. 要求 `len(model-states) = len(model-events) + 1`，第 0 个状态是初始 TLC 状态；
2. 使用 artifact 中的 model config 创建与执行时相同的 Raft Mapper；
3. 依次把每个持久化 `StepRecord` 重新映射，逐条 JSON 等价比较映射结果与
   `model-events.json`；名称、参数或边界不一致立即报错；
4. 一个 step 映射出 N 个 model event 时，按 Mapper 顺序将 event 1..N 分别对齐到
   后续 state 1..N；相同 concrete Action/Effects 附着到这 N 个 frame，事件特有的
   Snapshot outcome 仍按各 event 计算；
5. Partition、Heal 或被模型视为 stutter 的 step 映射出 0 个 event，此时生成一个
   不推进 model-state cursor 的 stutter frame，以保留网络和生命周期事实；
6. 全部 step 结束后再次检查 event/state cursor 必须恰好耗尽。

规则不使用时间戳或模糊匹配，也不改变 replay。一个多 natural-timeout action 映射两个
Timeout event 的回归测试验证了逐 event 推进；错误的 state/event 数量和内容会明确
失败。

## 5. Network、Recovery 和 Snapshot 上下文恢复

分析器从 `result.initial_observation` 初始化消息队列和活动分区，然后按 Trace 顺序：

- Deliver/Drop 用持久化 MessageID、Link、Position 精确移除消息；
- Duplicate 按 Runtime 的单调 MessageID 规则复制；
- `EffectSendMessage` 加入消息；
- Partition 保存规范化分组；
- Heal 清除分区，并把当时仍跨阻断链路排队的消息标为 delayed，直到投递或丢弃；
- Crash/Restart 维护本次执行内的 restarted/recovering 集合，不使用固定 action 窗口；
- 重启节点只有在存在唯一最高 term active leader，且 log、commit 以及可用的 storage
  边界一致时，才保守地判定 recovery completed；
- 发往 restarted 节点的 `MsgApp`/`MsgSnap` 记录恢复方式，并比较消息 metadata term
  与投递前节点 term，得到 stale/same/higher；
- Snapshot model event 和明确的 Effect 恢复 created、pending、installed、
  rejected/stale、failed、retry-pending、retry-succeeded、fast-forward。

因此当前 32 份完整 artifact 可以确定性恢复这些上下文。无法从现有持久化数据证明的
瞬时内部阶段不会根据最终状态猜测。

## 6. Coverage Facet schema

每个 Facet 都有独立的结构、稳定 JSON、SHA-256 前 64 bit key、distinct count 和增长
曲线。五个 key 不会再拼成新的全局 key。

### 6.1 Election

字段包括 active role topology、active/crashed 数量 bucket、leader/candidate 数量、
active term topology、leader/candidate 相对最大 term 的位置、active quorum、
candidate vote boundary 和 votedFor 粗关系。它不包含绝对 term、完整日志、Snapshot
细节或节点 ID。

测试区分 stable/no leader、单/多 candidate、self-only/one-short/quorum、
same/split term，并验证绝对 term 平移和节点对称换名不改变 key。

### 6.2 Replication

字段包括 leader 数量、log topology、committed-prefix relation、lagging follower
数量、replication/commit lag bucket 多重集、catch-up topology、append catch-up、
snapshot required，以及未提交/已提交冲突。它不包含绝对 index、完整 entry、节点 ID
或投票细节。

测试区分 ordinary append 与 snapshot-required catch-up、未提交冲突和已提交冲突，
并验证绝对 index 平移不改变 key。

### 6.3 Snapshot

字段包括状态 mode、node snapshot phase 多重集、当前 frame outcome 和 retry pending。
mode 为 no snapshot、available、required、pending 或 not-modeled；outcome 可为 none、
created、pending、installed、delivered、rejected-or-stale、failed、retry-pending、
retry-succeeded、fast-forward。没有可靠事件时不会凭最终状态补造 outcome。

### 6.4 Recovery

字段包括：

- phase：normal、node-crashed、restarted-waiting-catch-up、
  recovery-completed、restarted-recovered；
- recovering 节点数量 bucket；
- recovery mode：restart、append-entries、snapshot、other-message 或 none；
- 恢复消息相对 term：stale/same/higher/unknown/none。

它使用执行前缀语义，不使用“最近 N 个 Action”的人工窗口。

### 6.5 Network

字段包括：

- no-partition、single-follower-isolated、leader-isolated、
  majority-minority-split、multi-group-partition、no-connected-quorum 或 healed；
- 按 group size、active、leader、candidate 数量规范化的 group shapes；
- 是否存在连通 quorum；
- leader 位于 majority/minority、被隔离、无 leader或 multiple leaders；
- 是否刚 Heal，以及是否仍有分区期间积压的 delayed message。

最终结构不保留节点 ID。

## 7. Interaction schema

只实现四个有明确含义的二元 interaction，没有实现五维全乘积：

| interaction | 字段 | 测试含义 |
|---|---|---|
| `election_network` | leader mode、主要 candidate vote 类、network mode、connected quorum | 分区是否阻断/改变选举进展 |
| `replication_network` | log topology、主要 catch-up 类、network mode、heal 后 delayed | 分区、Heal 与日志追赶的关系 |
| `snapshot_recovery` | snapshot mode/outcome、recovery phase | 重启节点是否通过 Snapshot 恢复，以及失败/重试 |
| `recovery_term_relation` | recovery phase、message term relation、term topology | 重启节点收到 stale/higher-term 消息时的协议状态 |

每个 interaction 也独立序列化和计数。

## 8. 实际离线分析

仓库内没有其他持久化 `model-states.json`。本轮直接复用第一轮仍保存在 `/tmp` 的 24 条
随机和 8 条 Snapshot/Partition 定向真实 etcd-raft + strict TLC artifact，没有重新
运行长时间 fuzz。

```bash
GOCACHE=/tmp/modelfuzz-ng-factorization-gocache \
GOPATH=/tmp/modelfuzz-ng-factorization-gopath \
go run ./cmd/modelfuzz-ng coverage-factorize \
  -input /tmp/modelfuzz-ng-coverage-v2-real-20260728/random \
  -output /tmp/modelfuzz-ng-factorization-random-final.json

GOCACHE=/tmp/modelfuzz-ng-factorization-gocache \
GOPATH=/tmp/modelfuzz-ng-factorization-gopath \
go run ./cmd/modelfuzz-ng coverage-factorize \
  -input /tmp/modelfuzz-ng-coverage-v2-real-20260728/snapshot-partition \
  -output /tmp/modelfuzz-ng-factorization-snapshot-final.json

GOCACHE=/tmp/modelfuzz-ng-factorization-gocache \
GOPATH=/tmp/modelfuzz-ng-factorization-gopath \
go run ./cmd/modelfuzz-ng coverage-factorize \
  -input /tmp/modelfuzz-ng-coverage-v2-real-20260728 \
  -output /tmp/modelfuzz-ng-factorization-combined-final.json
```

三次报告均为 `deterministic=true`。v1/v2 数量与第一轮完全一致。

### 8.1 完整状态

| 数据 | executions | model-state visits | CoverageFrames | v1 | v2 | v2 reduction | v2-new executions |
|---|---:|---:|---:|---:|---:|---:|---:|
| Random | 24 | 2,952 | 3,983 | 1,491 | 1,107 | 25.75% | 24/24 |
| Snapshot/Partition | 8 | 232 | 272 | 64 | 22 | 65.62% | 1/8 |
| 合并（路径排序） | 32 | 3,184 | 4,255 | 1,530 | 1,110 | 27.45% | 25/32 |

Frame 多于 model-state visit 是预期结果：零 model-event 的 Partition/Heal 等 concrete
step 仍产生 stutter frame。合并报告中 Snapshot 目录排在 Random 后面，因此合并
quartile 反映这个确定顺序，不应解释为随机抽样顺序。Random 中 v1 Q4/Q1 为 0.821，
v2 为 0.543；完整 v2 仍未饱和。

### 8.2 字段基数

合并数据的关键结果：

| 字段 | distinct | 观察 |
|---|---:|---|
| `top.canonical_node_shapes` | 1,110 | 与完整 v2 distinct 完全相同，近似唯一 |
| `node.full_class` | 532 | 9,552 个 node occurrence 中的完整 node 类 |
| `top.voting_topology` | 26 | 中等 |
| `top.catch_up_topology` | 22 | 中等 |
| `top.role_topology` | 13 | 有限 |
| `node.leader_peer_lags` | 11 | 组合值，不是单 bucket |
| `top.snapshot_topology` | 9 | 有限 |
| `node.inbound_catch_ups` | 8 | 有限 |
| `node.voted_for` | 6 | 有限 |
| `commit_lag` / `applied_lag` | 各 4 | 单独很小，但与其他字段组合后影响大 |
| `term_topology` | 4 | 有限 |
| `log_topology` | 3 | 有限 |
| `lifecycle` | 2 | 有限 |
| `cluster_size`、`committed_prefixes`、`quorum_available` | 各 1 | 当前样本恒定 |

`applied_lag=zero` 占 8,940/9,552 次，`lifecycle=active` 占 9,390/9,552 次。
低单字段基数仍能因 node shape 笛卡尔组合产生大量类。

### 8.3 单字段和字段组消融

完整 v2 为 1,110。主要结果：

| 删除内容 | 删除后 distinct | 基数贡献 |
|---|---:|---:|
| `canonical_node_shapes` | 366 | 67.03% |
| lag 组：commit/applied/replication lag | 484 | 56.40% |
| node `commit_lag` | 765 | 31.08% |
| node `applied_lag` | 808 | 27.21% |
| Snapshot 组 | 837 | 24.59% |
| log/catch-up 组 | 907 | 18.29% |
| voting 组 | 1,036 | 6.67% |
| node `log_relation` | 1,060 | 4.50% |
| role/term 组 | 1,072 | 3.42% |
| node `leader_peer_lags` | 1,081 | 2.61% |

以下单独删除时为 0%：所有顶层汇总字段、node lifecycle、node role、node
candidate_votes，以及“可能由 node shapes 重复表达的顶层汇总字段”整组。这不表示
它们没价值，而是证明 node shapes 已经保存了足以区分当前样本的重复事实。

最大的条件分裂：

- 去掉 lag 组后，同一个粗状态最多被完整 lag 组合分裂成 24 个 v2 状态；
- 去掉 canonical node shapes 后，同一组顶层摘要最多被 node shapes 分裂成 29 个；
- 去掉单独 commit lag 后，最多分裂成 10 个；
- 去掉 log/catch-up 组后，最多分裂成 7 个；
- 去掉 Snapshot 组后，最多分裂成 7 个；
- 去掉 role/term 组后，最多分裂成 5 个；
- 去掉 voting 组后，最多分裂成 3 个。

例如同样的非 lag 事实下，`commit_lag` 的 zero/one/small/large 与不同节点分配形成多达
10 个完整状态；同样的非追赶事实下，append-large、snapshot-required 和
snapshot-pending 又产生 7 个状态。这些是有意义的协议差别，但不适合全部相乘后只用
“全局新/旧”表示测试进展。

### 8.4 Facet 和 interaction

Random 24-run：

| Facet | distinct | 有新增的执行 | 最后新增 | Q1/Q2/Q3/Q4 新值 | Q4/Q1 |
|---|---:|---:|---:|---|---:|
| Election | 63 | 15/24 | 24 | 39 / 5 / 15 / 4 | 0.103 |
| Replication | 228 | 23/24 | 24 | 101 / 77 / 36 / 14 | 0.139 |
| Snapshot | 33 | 11/24 | 24 | 27 / 3 / 2 / 1 | 0.037 |
| Recovery | 28 | 9/24 | 19 | 19 / 3 / 4 / 2 | 0.105 |
| Network | 28 | 14/24 | 20 | 14 / 7 / 5 / 2 | 0.143 |

Random interaction distinct：Election×Network 26、Replication×Network 80、
Snapshot×Recovery 48、Recovery×TermRelation 37。

Snapshot/Partition 8-run：

| Facet | distinct | 有新增的执行 | Q1/Q2/Q3/Q4 |
|---|---:|---:|---|
| Election | 6 | 1/8 | 6 / 0 / 0 / 0 |
| Replication | 14 | 1/8 | 14 / 0 / 0 / 0 |
| Snapshot | 9 | 1/8 | 9 / 0 / 0 / 0 |
| Recovery | 2 | 1/8 | 2 / 0 / 0 / 0 |
| Network | 4 | 1/8 | 4 / 0 / 0 / 0 |

定向组五个 Facet 都在第一条执行后完全饱和，Snapshot Facet 的结论尤其明确；完整 v2
也只有 1/8 执行新增，但 Facet 能进一步说明“是哪个行为维度没有新增”。

在 Random 中，Snapshot 最接近饱和，Recovery 和 Network 在第 19/20 条后没有新增；
Election、Replication 仍在第 24 条新增，其中 Replication 23/24 条都有新增，是当前
仍持续增长最明显的切面。不能因为 Facet 数量较少就宣称它们已经足够好。

## 9. 对评估问题的回答

1. **v2 增长主要来源**：canonical node shapes 及其中的 lag 组合，其次是 Snapshot 和
   log/catch-up 组合。
2. **canonical node shapes 是否最大**：是。它有 1,110 种，删除后降至 366。
3. **重复表达**：role/lifecycle/quorum、vote、snapshot phase、log relation 和
   catch-up 均在顶层摘要与 node shapes 中重复；删除顶层重复组时基数完全不变。
4. **随机中趋于饱和的 Facet**：Snapshot 最明显；Recovery、Network 也明显降速并较早
   停止新增。
5. **仍持续增长的 Facet**：Replication 最明显；Election 在最后一条仍有新增。
6. **定向 Snapshot 是否快速饱和**：是，9 个 Snapshot 值全部来自第 1 条。
7. **Network/Recovery 能否稳定恢复**：对这 32 份完整 artifact 可以，重复分析一致；
   但依赖 initial observation、完整 Trace/Effects 和当前 MessageID 分配约定。
8. **Facet 是否更可解释**：是。它能明确指出新增来自复制而非网络/恢复，或定向
   Snapshot 已重复；完整 v2 只能报告整个组合是否新。
9. **goal-local stall 候选**：Snapshot、Recovery、Network 已可作为候选的局部进度
   信号；Replication 需要结合具体 goal，不能直接把 228 个值当成统一 stall。
10. **继续删 v2 还是进入 Goal/Waypoint**：证据不支持继续全局删 v2 字段。v2 应保留
    为诊断结构，下一轮更适合用少量人工目标验证局部 Facet 是否真正改善找 bug 能力。

## 10. 回归测试和验证

新增测试覆盖：

- Facet 稳定序列化、重复投影、节点对称换名；
- Election 的 term 平移、leader/candidate/vote/term 边界；
- Replication 的 index 平移、append/snapshot 追赶、未提交和已提交冲突；
- Snapshot available 及 pending/installed/failed/retry/fast-forward outcome；
- Recovery normal/crashed/recovering/completed；
- Network 无分区、follower/leader isolated、无连通 quorum、Heal 后 delayed；
- CoverageFrame 的零 event stutter、多 event 顺序对齐和错误拒绝；
- 完整 factorization 重复结果一致；
- CLI 只读、缺失上下文报错、输出目录隔离；
- 原 `coverage-compare` 和 v1/v2 固定序列化测试继续存在。

最终验证命令：

```bash
GOCACHE=/tmp/modelfuzz-ng-factorization-gocache \
GOPATH=/tmp/modelfuzz-ng-factorization-gopath \
go test ./...

GOCACHE=/tmp/modelfuzz-ng-factorization-gocache \
GOPATH=/tmp/modelfuzz-ng-factorization-gopath \
go vet ./...
```

实际执行结果：`go test ./...` 全部通过；`go vet ./...` 无输出并以状态 0 结束。测试使用
Go 1.26.4 和上面固定的 `/tmp` cache/GOPATH。

## 11. 已知限制

- 只分析保存了完整逐运行 artifact 的执行；`runs.jsonl` 或聚合 report 不足以恢复。
- Trace 不直接保存每一步完整消息队列。分析器从 initial observation 和顺序
  Action/Effect 精确重放队列；若未来 Runtime 修改 Duplicate 的 MessageID 分配约定，
  分析器必须同步版本化。
- 多 model-event step 的调度器上下文是 action 完成后的统一上下文，不声称恢复 action
  内部每个 Effect 之间不可见的瞬时消息队列。
- Recovery completion 是保守判定：没有唯一最高-term active leader 时不会宣布完成；
  TLC 不含 epoch，因此无法表达跨执行的“曾经重启”。
- `committed_prefixes` 和 `quorum_available` 在本数据中恒定，无法用这批正常轨迹评价它们
  对 bug 的区分能力。
- scenario 标签允许重叠。Random 轨迹同时包含 election、partition、snapshot 等事件，
  所以这些标签是子集描述，不是相互独立的实验组。
- 当前只有 3 节点、24+8 条轨迹；还没有 5 节点、其他 Raft 实现或外部 issue benchmark
  上的 Facet 效果证据。
- Facet 饱和只表示这些抽象值没有新增，不等于代码覆盖充分，更不等于没有 bug。
- 本轮没有把 Facet 接入 Corpus，因此没有证明它能提升找 bug 速度或可靠性。

## 12. 下一轮建议

选择 **B：开始实现少量人工 Behavior Goal 和 Waypoint Predicate**。

理由不是“Facet 数量少”，而是本轮已找到完整 v2 持续增长的结构性原因：合理维度在
canonical node shapes 中形成组合积，继续删全局字段会混淆“降低数字”和“保留 bug
相关语义”。与此同时，Snapshot/Recovery/Network 已表现出明显局部饱和，能够为少量
具体目标提供可解释进度；Replication 仍持续增长，正适合在目标内约束上下文后再判断
stall。

建议下一轮只选少量人工目标，例如“leader 隔离后重新选举并提交”“落后 follower 必须
通过 Snapshot 恢复”“重启 follower 收到 stale/higher-term 消息后收敛”。用现有 Facet
作为 goal-local 观测，不替换全局 v1/v2，也先不让 LLM 自由生成目标。是否接入 Corpus
或 LLM，应由这些目标上的 bug 命中率、达到目标的时间和重复执行比例决定。

本轮不自动实施该建议。
