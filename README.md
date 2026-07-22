# ModelFuzz-NG

ModelFuzz-NG 是一个正在从零实现的、面向分布式系统的模型引导模糊测试框架。
当前目标是先跑通 etcd-raft 的最小闭环：高层 Plan 在线解析为 Concrete Action，
Runtime 控制逻辑时间和消息队列，Adapter 驱动真实 Raft，随后把 Trace 映射到
轻量 TLA+ 模型。

## 当前模块

- `internal/core`：协议无关的 Action、Effect、Observation、Message 和 Trace。
- `internal/runtime`：单次可重放执行、逻辑时钟、消息队列和资源预算。
- `internal/plan`：高层 Plan 及其基于当前状态的在线解析。
- `internal/engine`：Plan、Runtime、模型映射和模型执行的单次闭环编排。
- `internal/policy`：在线随机基线，以及严格校验 LLM Plan 的生成策略。
- `internal/corpus`：只保留触发全局新模型状态的 Plan 和增量覆盖键。
- `internal/mutation`：Corpus Plan 的本地随机变异和可选 LLM 变异。
- `internal/llm`：厂商无关 JSON 补全接口及多 provider OpenAI-compatible 客户端。
- `internal/experiment`：候选执行、覆盖反馈、Corpus 保留和异步变异闭环。
- `internal/metrics`：协议无关的执行明细、耗时、吞吐和覆盖增长统计。
- `internal/minimize`：保持稳定失败签名的 Plan ddmin、单 Action 固定点缩减、候选缓存和中断恢复。
- `internal/persistence`：原子 JSON、追加式 JSONL 和崩溃尾记录修复。
- `internal/adapters/etcdraft`：etcd-raft 3.7 的最小集群适配器。
- `internal/model`：Concrete Transition 到模型事件的映射及 TLC 客户端。
- `internal/oracle`：对真实 Concrete Transition 执行基础在线安全性检查。
- `cmd/modelfuzz-ng`：读取配置和 Plan、执行轨迹并保存产物的命令行入口。
- `models/raft`：首版轻量 Raft TLA+ 模型。
- `docs`：Timer 设计与目标目录结构。

实验记录的主题索引和 `runs/` 原始产物保留规则见
[`docs/experiments/README.md`](docs/experiments/README.md)。
系统执行流程、Raft 事件语义、典型实验结果、与原始 ModelFuzz 的双向能力对照，以及原论文所称两个 etcd bug 的证据边界见 [`docs/system-overview-and-modelfuzz-comparison.md`](docs/system-overview-and-modelfuzz-comparison.md)。

## 本地依赖

当前 `go.mod` 使用：

```go
replace go.etcd.io/raft/v3 => ../raft
```

因此 `modelfuzz-ng` 和修改后的 `raft` 目录需要同级放置。Raft 应基于 `v3.7.0`
（release 3.7），并包含 Adapter 所需的实例级 `Config.Rand` 注入接口。Raft fork
尚未发布前，仅克隆本仓库不能独立编译；这是当前阶段的已知部署约束。

## 运行最小闭环

不连接 TLC 时，CLI 仍会执行真实 Raft 并生成模型事件：

```bash
go run ./cmd/modelfuzz-ng run \
  -config examples/config.json \
  -plan examples/plans/election.json \
  -output runs/election-local
```

连接模型时，直接启动仓库内自带的严格 controlled TLC 服务：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft.tla \
  --config models/raft/raft-5.cfg \
  --port 2023
```

首次运行会下载并校验官方 TLA+ Tools v1.8.0，再编译 NG 自己维护的 Java 服务层；
不再依赖原 ModelFuzz artifact。该服务严格拒绝无法映射、当前状态下不可执行、产生
多个后继或违反模型 invariant 的事件，不会把它们静默当作 stutter。服务启动后，
CLI 增加：

```bash
-tlc http://127.0.0.1:2023
```

`raft-5.cfg` 对应 5/5 烟雾边界，`raft-10.cfg` 对应原 ModelFuzz 主实验使用的
`LargestTerm=10`、`MaxLogIndex=10`。CLI 可用 `-largest-term` 和
`-max-log-index` 覆盖 JSON 配置；严格 TLC 的 `/health` 会返回实际 cfg 的 Server、LargestTerm、MaxLogIndex、MaxValue 和 Nil，CLI 在执行前自动拒绝节点集合或边界与 Go Mapper/随机策略/LLM 配置不一致的组合；连接旧 health schema 时会明确警告无法核对 Server/MaxValue。恢复实验
禁止修改这两个边界。10/10 长跑配置见 `examples/config-soak-10.json`。
服务不再在启动时枚举数百万个参数化 Action；它在收到事件后才绑定具体
Action，并用有界 LRU 缓存复用热点组合，因此10/10不再需要依赖超大 JVM heap。

五节点选举 quorum mutant 可通过 `examples/config-quorum-mutant.json` 显式启用，
`-vote-quorum-divisor 2` 表示正常多数派，`3` 表示复现 `n/3+1` 人工缺陷。三节点下
两个阈值相同，因此 divisor=3 至少需要4个节点；五节点10/10模型使用
`models/raft/raft-5nodes-10.cfg`。恢复实验禁止修改该 FaultPolicy。

每次运行必须使用一个尚不存在的输出目录，CLI 不会覆盖旧轨迹。目录中包含
解析结果、Concrete Action、Trace、模型事件、模型状态、Oracle Finding、`failure.json`
以及汇总结果。成功时 `failure.json` 为 `null`；同步 Adapter/SUT 调用发生
panic 时，其中保存失败操作、逻辑时间、失败 Action、执行前 Observation、panic 值
和 goroutine 堆栈。失败 Action 不会被写成虚假的完整 Step，已成功的 Trace 前缀仍会持久化。
当前 Adapter 已支持可配置的 snapshot/日志压缩与 crash/restart。轻量 Raft 模型
没有 snapshot 状态，因此快照维护 Effect 和 `MsgSnap` 在该 Profile 中明确分类为
stutter，不声称它验证了 InstallSnapshot 协议。动态 membership change 仍未完整支持。

Profile 预检把非成功结果分为三类：动作与当前状态不匹配记为 `inapplicable`
no-op；确定会越过有限模型 term/log 上界时以 `model_bound_reached` 正常结束已有
前缀；只有当前模型确实没有语义或消息元数据损坏时才使用
`unsupported_by_model` 失败。每个判定同时带稳定的 `reason_code`，统计不再依赖
包含节点号或边界值的动态错误文本。报告提供全局 `decision_counts` 和按候选来源
拆分的 `decision_counts_by_source`。

## 当前能力声明

这里的“可执行”表示 Runtime/Adapter 能驱动真实 etcd-raft；“可映射”表示当前
`models/raft/raft.tla` Profile 能接收对应语义。模型引导实验只能使用两列都支持
的能力。

| 能力 | Runtime/Adapter | 当前模型与Mapper | 说明 |
|---|---|---|---|
| Deliver/Drop/Duplicate | 支持 | 支持 | Drop/Duplicate 只改变受控网络，对模型是 stutter |
| AdvanceTime/自然超时 | 支持 | 支持 | 一单位时间对应每个存活节点一次 Tick |
| 强制选举超时 | 支持 | 支持 | 自然/强制来源都映射为 `Timeout` |
| Client Request | 支持 | 支持 | Leader 直接接收；Follower 向已知 Leader 转发；无 Leader/Candidate 的拒绝记录为模型 stutter |
| crash/restart | 支持 | 支持 | 崩溃保留稳定状态；恢复时增加 epoch 并重置 Raft 易失状态 |
| network partition/heal | 支持 | 明确 stutter | 跨组消息保留在确定性队列并标记 blocked；合并后按原 MessageID/顺序恢复投递 |
| snapshot/log compaction | 支持，默认关闭 | 明确 stutter | Adapter 模拟应用层维护；`MsgSnap` 仍走受控网络 |
| dynamic membership change | 仅保留 etcd-raft 基础处理 | 不支持 | 固定 voter 实验之外尚未验证 ConfState snapshot |
| PreVote/CheckQuorum | 当前关闭 | 不支持 | 启用 Raft 配置前必须先补模型 |

Raft Observation 额外暴露 `committed_prefix_available` 和
`committed_prefix_digests`。摘要以确定性 protobuf 编码计算，并只携带当前集群
commit 索引所需的检查点，避免长日志在每个 Observation 中全量展开。基础
Raft Oracle 因此可以比较两节点 `min(commitA, commitB)` 处的共同已提交前缀，
不再受未提交尾部影响，也会检查 crashed 节点保留的稳定日志。
开启快照后，这些摘要由应用层的逻辑 committed prefix 继续维护，不再从 index=1 读取已压缩的 `MemoryStorage` 日志。Snapshot Data 是确定性 JSON，保存截至 snapshot index 的链式前缀摘要；Effect 和统计只保存 index/term/size，不重复载入 payload。Oracle 还检查 `snapshotIndex <= applied <= commit <= lastIndex`、`firstIndex <= snapshotIndex+1`、snapshot term 合法性，以及 term、commit、applied、snapshot index、first index 在 crash/restart 前后不回退。

### Snapshot 策略

etcd-raft 不会为应用自动调用 `CreateSnapshot` 或 `Compact`。NG Adapter 用已应用
日志数量模拟这个应用层维护策略：

```json
"snapshot": {"threshold": 2, "retain_entries": 0}
```

`threshold=0` 是默认值，表示关闭；启用后，当
`applied-lastSnapshotIndex >= threshold` 时在 applied index 创建快照，并在
`snapshotIndex-retain_entries` 压缩。`run`/`experiment` 可用
`-snapshot-threshold` 和 `-snapshot-retain-entries` 覆盖；恢复 checkpoint 时禁止改变。

每个运行节点还暴露 `election_elapsed`、`election_timeout`、
`randomized_election_timeout`、`election_ticks_remaining` 以及对应的 heartbeat
计数。Profile 因而可以准确判断下一次 tick 是否会触发自然选举超时；在线随机
策略只生成不会立即越过当前 TLA+ term 上界的单 tick 动作。

消息分类：

| 消息 | 当前处理方式 |
|---|---|
| `MsgVote`、`MsgVoteResp` | 映射为选举模型动作 |
| `MsgApp`、`MsgAppResp` | 映射为复制和确认动作；成功的多 entry 批次按日志顺序展开为单 entry 模型事件 |
| `MsgHeartbeat` | 映射为无 entry 的 `MsgApp`，保留 term、角色和 commit 传播 |
| `MsgHeartbeatResp` | 当前 Profile 中明确 stutter |
| `MsgReadIndex`、`MsgReadIndexResp` | 只读状态未进入模型，明确 stutter |
| `MsgProp` | Follower 转发 proposal 时进入受控网络；投递到当前 Leader 才映射为 `ClientRequest`；Leader 变化后的 `ErrProposalDropped` 记录 `raft.proposal_dropped` 并 stutter |
| `MsgSnap` | 由 Raft 在 follower 的 nextIndex 早于 FirstIndex 时产生，经 Runtime 延迟/丢弃/复制/投递；基础模型明确 stutter |
| `MsgTimeoutNow`、`MsgPreVote` 等其他网络消息 | 不支持并返回错误，不会静默忽略 |
| `MsgHup`、`MsgBeat` 等本地消息 | 不进入 Runtime 网络队列 |

示例 Plan：

- `election.json`：只完成节点 1 的选举；
- `election-commit-node1.json`：节点 1 当选并把 no-op 复制到节点 2后提交；
- `election-commit-node2.json`：节点 2 当选并把 no-op 复制到节点 3后提交；
- `client-request-commit.json`：节点 1 当选，随后复制并提交 no-op 和请求值 `1`。
- `follower-request-forwarding.json`：Follower 接收请求，通过受控 `MsgProp` 转发给
  Leader 后复制并提交。
- `follower-catchup-multi-entry.json`：节点 2 落后三条客户端日志后，通过单个
  多 entry `MsgApp` 追赶并提交。
- `follower-crash-restart.json`：follower恢复后处理延迟消息并追赶日志；
- `leader-crash-reelection.json`：leader停止后重新选举，旧leader恢复并追赶；
- `uncommitted-log-restart.json`：未提交日志跨恢复保留并在新term提交；
- `committed-log-restart.json`：检查已提交日志和commit跨恢复保持；
- `repeated-crash-restart.json`：检查重复生命周期动作的best-effort解析。
- `snapshot-normal.json`：提交 no-op 和请求后创建 snapshot 并压缩；
- `snapshot-follower-catchup.json`：follower 离线时 Leader 压缩，恢复后通过 MsgSnap 追赶；
- `snapshot-duplicate-delivery.json`：复制 MsgSnap，验证重复投递稳定归类为 stale。
- `network-partition-merge.json`：隔离旧 Leader，连通组内重新选举和提交，合并后投递积压消息并收敛。

端到端回归还覆盖：Follower 安装 index=2 的 snapshot 后保留旧副本，Leader 继续压缩并发送 index=4 的 snapshot；Follower 先安装新 snapshot、再收到旧副本时必须拒绝旧 snapshot，随后再次 crash/restart 仍保持 snapshot 边界和 committed prefix。

网络分区使用 `{"kind":"partition","partition":{"groups":[[1],[2,3]]}}`，每个节点必须恰好属于一个组；`{"kind":"heal"}` 合并当前分区。分区期间跨组消息继续入队但不能 Deliver，Drop/Duplicate 仍可控制队列；Observation 为这些消息记录 `blocked=true`。在线随机策略和本地 Mutation 分别提供可配置的 partition/heal 权重及成对插入比例，checkpoint v8 将这些参数纳入恢复边界。

定向在线策略可用 `experiment -initial-policy snapshot-partition` 启动。它根据每一步最新 Observation 选举 Leader、隔离一个固定 lagger、在多数派提交并压缩日志，然后仅在 `leader.first_index > lagger.last_index+1` 时 heal，确保增量复制窗口确实已消失，再驱动 `MsgSnap` 发送、可选复制、应用和 stale 投递。策略不预测 MessageID；当前默认隔离最大非 Leader 节点、只生成二分区、复制一份 snapshot、使用16个 recovery tick，并只保证目标 lagger 追赶 Leader，不宣称所有其他 Follower 已完全排空。历史 3/5 节点多 seed 结果使用 `retain_entries=0`；端到端回归另覆盖 retain=1 和 retain=threshold，若 MaxLogIndex 不足以压缩过 lagger 会在启动时明确失败。

失败 Plan 可用以下命令缩减：

```bash
go run ./cmd/modelfuzz-ng minimize \
  -plan runs/failure/plan.json \
  -config runs/failure/config.json \
  -output runs/failure-minimized \
  -final-verify-runs 3
```

缩减器先重复确认原始失败签名，再执行 ddmin 和单 Action 固定点删除；TLC 签名比较稳定 error code 与模型动作名而忽略 event index，普通 runtime error 比较归一化根因类别，panic 精确比较 panic value，Oracle 比较排序去重后的 code 集合。输出保留用户原 Metadata，缩减信息单独写入 `minimization-report.json`；`one_minimal=true` 只表示任意单个 Action 都不能继续删除，不等于全局最短。长任务会原子保存 `minimization-checkpoint.json`，可用 `minimize -resume DIR` 继续；checkpoint 包含当前 Plan、尝试数和候选 digest 缓存，报告记录 Plan/config SHA-256 与最终稳定复现次数。未显式提供 `-config` 且 Plan 同目录没有 `config.json` 时命令会拒绝运行。

上述完整 Plan 已使用真实 etcd-raft 和 controlled TLC 运行，结果见
[`docs/experiments/basic-raft-20260720.md`](docs/experiments/basic-raft-20260720.md)。
四条 Plan 的基础 Raft Oracle 检查及故障注入结果见
[`docs/experiments/raft-oracle-20260720.md`](docs/experiments/raft-oracle-20260720.md)。
panic 捕获、committed-prefix 和轨迹兼容性实验见
[`docs/experiments/panic-prefix-20260720.md`](docs/experiments/panic-prefix-20260720.md)。
节点生命周期、恢复、重新选举、持久日志和随机策略实验见
[`docs/experiments/crash-restart-20260721.md`](docs/experiments/crash-restart-20260721.md)。
非 Leader 请求转发、稳定原因统计和 timer 状态实验见
[`docs/experiments/nonleader-timer-reasons-20260721.md`](docs/experiments/nonleader-timer-reasons-20260721.md)。

严格重放已有运行时，默认从 `trace.json` 同目录读取 `config.json`：

```bash
go run ./cmd/modelfuzz-ng replay \
  -trace runs/basic-raft-20260720/client-request-commit/trace.json \
  -output runs/replay-client-request
```

Replay 会逐步检查逻辑时间、MessageID/Link/Position、Effect、节点快照和
ObservationDigest，并在第一处差异停止。当前 Trace 版本为 v4；Replay 会在比较时
移除新增的 committed-prefix 观测字段，因此仍能重放 v2/v3 轨迹。四条完整示例的实际重放分别匹配
`6/6`、`6/6`、`9/9` 和 `11/11` 个步骤。
五条节点生命周期示例另外严格匹配合计 `54/54` 个步骤。

运行默认反馈实验：

```bash
go run ./cmd/modelfuzz-ng experiment \
  -config examples/config.json \
  -output runs/random-local \
  -runs 20 -max-plan-actions 30 -parallelism 4 -seed 1000
```

`-runs` 现在表示闭环中的总执行次数，不再表示彼此独立的随机实验。默认配置下，
初始种子仍由在线随机策略逐步读取最新 Observation 产生，因此不会缓存容易失效的
MessageID。原始 TLC fingerprint 继续完整计入覆盖统计；默认只有一次成功执行至少
增加25个全局未见的 `State.Key`，并且同时增加归一化 Raft 语义状态或语义转移时，
才会携带 Plan 和增量覆盖键进入 Corpus。可用 `-min-new-model-states` 调整原始门槛，
`-semantic-coverage=false` 仅用于对照实验。语义投影保留活动节点、角色、相对 term、
日志形状、提交/复制滞后和投票关系，同时忽略绝对 term 与 nextIndex 内部记账差异。
完整 Trace 由逐运行
产物策略单独保存，不再重复写入 Corpus 和 checkpoint。候选按 FIFO 继续执行。
当前默认每个新状态生成1个本地随机变异、每条 Corpus 最多2个；Ready
队列默认上限为4096，可用 `-max-ready-candidates` 调整。队列满时确定性淘汰最旧候选，
优先保留新候选。变异在独立 goroutine 中产生，可以和已经排队的 Plan 执行重叠。
每条新实验轨迹默认最多生成1000个 PlanAction；在线随机动作通常一对一解析为
Concrete Action，消息批量选择可能一对多展开，最终仍受 `runtime_limits.max_actions`
约束。短烟雾实验可显式传入较小的 `-max-plan-actions`。
离线随机 Mutation 还会主动用 `crash(node) ... restart(node)` 包围一段已有动作，
默认选择概率为5%，可用 `-crash-restart-pair-percent` 调整。
它也会默认以5%概率插入覆盖至少一个已有动作的 `partition ... heal` 对，可用 `-partition-heal-pair-percent` 调整；在线策略对应 `-partition-weight` 和 `-heal-weight`，默认2/8。
候选入队前会检查节点生命周期和同时停止节点上限，不会生成重复 crash 或未 crash
就 restart 的配对。
在线 balanced 随机策略会根据最新节点状态生成 crash/restart，默认 crash 权重为1，
同时最多停止1个节点且不会停止最后一个运行节点；每条轨迹最多4个 crash 周期，
相邻周期至少间隔48个 Action，Restart 不受 cooldown 限制。对应参数为
`-crash-weight`、`-restart-weight`、`-max-crash-episodes` 和 `-lifecycle-cooldown`。
发往停止节点的在途消息可在恢复后继续参与调度。
它也会向已知当前 Leader 的 Follower 生成客户端请求，并把产生的 `MsgProp` 作为
普通受控消息调度；Candidate 或尚不知道 Leader 的 Follower 不进入随机请求候选集，
避免把预算浪费在确定会被丢弃的 proposal 上。
默认动作权重将 Deliver 提高到60、强制 timeout 降到5；一次 timeout 后4个动作内
不再生成新的强制 timeout，已有 Leader 时其权重还会进一步降到四分之一。非空链路
上的过期消息 Start 会钳制到当前最后一个位置，并记录 `selector_start_clamped`；空链路
仍记录 `message_not_available`，Concrete Trace 始终保存最终 MessageID 和位置。

为避免反馈队列长期只围绕早期 Corpus 分支持续局部变异，可以按完成执行数周期性
注入新的在线随机种子：

```bash
-random-seed-interval 100 -random-seeds-per-interval 2
```

默认 `-random-seed-interval 0`，即关闭注入。到达阈值后，新种子优先取得后续空闲
执行槽位，但不会清空已有 mutation 队列；尚未执行的变异会在种子之后继续参与调度。
注入阈值和待注入数量都进入 checkpoint，因此恢复不会重复或跳过注入。

不连接 TLC 时没有模型状态可用于反馈，命令仍可作为随机基线运行，但会给出警告且
`corpus.json` 保持为空。当前严格 controlled TLC 逐请求串行执行；连接它时建议使用
`-parallelism 1`，避免创建不能提升模型吞吐的并发请求。
实验根目录新增：

- `corpus.json`：最终紧凑 Corpus 摘要，只含原始/语义状态/语义转移覆盖键和条目数；
- `corpus.jsonl`：完整 Corpus Entry 的 fsync 追加日志；恢复时与 `runs.jsonl` 一样按
  checkpoint 水位修复并截断孤儿尾记录；
- `experiment-settings.json`：初始化、变异和 LLM provider 的非敏感配置；
- `llm-stats.json`：启用 LLM 时的调用、失败、累计时延和 token 统计，并通过
  `by_purpose.initial`/`by_purpose.mutation` 分开记录两条路径；该文件随 checkpoint
  更新，恢复后继续累计而不是覆盖；
- `experiment-report.json`：不含逐运行明细的闭环汇总；
- `runs.jsonl`：每条完成运行的候选关系、覆盖增量、稳定摘要和局部 Metrics；每次
  append 都会 flush 和 fsync，恢复时按 checkpoint 的已提交条数截去孤儿尾记录；每条
  记录包含 `new_raw_states`、`new_semantic_states`、`new_semantic_transitions`、
  `corpus_admission` 和 `semantic_novelty_per_100_actions`；
- `experiment-metrics.json`：Action、Effect、出站消息类型、解析结果及稳定原因码、模型事件、Oracle、失败、
  timer、终止原因、耗时分位数、吞吐、队列峰值和原始/语义覆盖增长曲线；同时统计唯一 Plan、
  唯一 Concrete Trace、唯一模型状态路径、重复率、唯一性增长曲线及各候选来源的首次发现贡献；
  `admitted_mutations`、`discarded_mutations` 和 `peak_ready_candidates` 用于观察反馈背压；
  `rejected_raw_threshold`、`rejected_no_semantic_novelty`、
  `retained_by_semantic_state`/`transition` 和 `corpus_admission_counts` 用于解释 Corpus 准入；
- `progress.jsonl`：完成执行的索引、完成变异和实验状态切换等轻量 fsync 生命周期日志；
- `checkpoint.json`：只保存 Corpus 原始/语义覆盖键与条目水位、增量聚合统计、唯一状态/Plan/Trace/路径集合、
  有界候选队列、运行中候选、紧凑待处理变异引用和随机编号；不保存完整 Run 或 Corpus Entry；
- `tlc-server-metrics.json`：严格 TLC 服务在每段启动/恢复执行前后的累计计数，包含
  Action 查询、后继计算、invariant 校验和状态序列化耗时；
- 每个被保存的 `run-*` 目录包含 `candidate.json` 和 `run-summary.json`。

长时间实验可以控制逐运行产物规模：

```bash
# all 保存全部；retained 保存新增覆盖和失败；failures 只保存失败；summary 不保存 run 目录。
-artifact-policy retained -checkpoint-every 10
```

收到 SIGINT/SIGTERM 后，Runner 会停止调度并强制写入最后一个检查点。之后使用：

```bash
go run ./cmd/modelfuzz-ng experiment -resume runs/random-local \
  -artifact-policy retained -checkpoint-every 10
```

恢复会继续使用原来的 run index、seed、Corpus 和候选队列；中断时尚未完成的候选
允许确定性重跑。`runs.jsonl` 或 `corpus.jsonl` 已写但未进入 checkpoint 的记录会先被
截去再重跑，不会形成重复 run index 或 Corpus ID。当前 checkpoint 格式版本为 v8；检查点包含实验配置指纹，修改
SUT、Engine、Policy 或 Mutator 配置后不能误接着旧实验运行。JSONL 最后一条若因
进程崩溃只写入了一部分，重新打开时只截去该不完整尾记录。

两个 LLM 开关相互独立且默认关闭：

```bash
# 这里只展示占位符；实际 Key 仅在本地环境变量中设置，不要写进仓库。
export DEEPSEEK_API_KEY='<replace-with-local-key>'

go run ./cmd/modelfuzz-ng experiment \
  -config examples/config.json \
  -tlc http://127.0.0.1:2023 \
  -output runs/llm-feedback \
  -runs 100 -max-plan-actions 50 \
  -llm-init -llm-mutate
```

- `-llm-init`：使用所选 provider 生成初始静态 Plan；关闭时使用在线随机策略；
- `-llm-mutate`：使用所选 provider 变异新 Corpus 条目；关闭时使用本地随机变异；
- `-llm-provider`：`deepseek`、`glm`、`qwen` 或 `kimi`，默认 `deepseek`；
- `-llm-model`、`-llm-base-url`：覆盖 provider 预设的模型和 API 基础地址；
- `-llm-api-key-env`：覆盖 API Key 的环境变量名，不接受直接的 Key 参数；
- 初始化允许思考模式以提高种子质量，时延敏感的变异明确使用非思考模式；
- LLM Prompt 包含 crash/restart 的状态前置条件和同时停止节点上限；输出必须通过
  严格 JSON、动作 schema、节点生命周期、节点集合、term 和 log index 边界校验；
- `request` 可以面向 Leader，也可以面向已经获知当前 Leader 的 Follower；后者只有在
  `MsgProp` 被调度到 Leader 时才真正进入模型。Candidate 或无已知 Leader 的节点会由
  etcd-raft 明确丢弃 proposal，并记录为成功执行后的模型 stutter。

当前 provider 预设如下，实验主路径仍为 DeepSeek：

| provider | 默认模型 | API Key 环境变量 |
| --- | --- | --- |
| `deepseek` | `deepseek-v4-flash` | `DEEPSEEK_API_KEY` |
| `glm` | `glm-5.2` | `ZHIPUAI_API_KEY` |
| `qwen` | `qwen-plus` | `DASHSCOPE_API_KEY` |
| `kimi` | `kimi-k2.6` | `MOONSHOT_API_KEY` |

Qwen 的最佳 Base URL 与百炼地域和业务空间有关，实验时建议用
`-llm-base-url` 填入控制台显示的实际地址。Kimi 默认使用可切换思考模式的
`kimi-k2.6`；如果改为始终推理的 Kimi K3，初始化使用 `max` 推理力度，
Mutation 使用 `low`，但仍无法像 K2.6 一样完全关闭思考。

当前实现不把 API Key 写入任何配置或实验产物。LLM 初始化失败会停止实验，LLM
变异失败则记录在报告中并在队列耗尽后继续补充种子，避免整个搜索永久卡住。
仓库根目录的 `.env.example` 只含无效占位字符串；真实 `.env` 已被忽略。

首次连接 DeepSeek 建议先做小规模初始化烟雾实验，不要直接运行上面的100次配置：

```bash
export DEEPSEEK_API_KEY='<local-key>'

go run ./cmd/modelfuzz-ng experiment \
  -config examples/config.json \
  -output runs/deepseek-init-smoke \
  -runs 2 -initial-population 2 -max-plan-actions 20 \
  -artifact-policy all -llm-init \
  -llm-provider deepseek -llm-model deepseek-v4-flash
```

这一步只调用一次初始化生成，不开启 LLM Mutation；检查 Plan 有效率、实际 Trace、
`llm-stats.json` 和失败产物后，再连接 TLC 并逐步开启 `-llm-mutate`。
本轮随机基线和 TLC 覆盖实验见
[`docs/experiments/random-baseline-20260720.md`](docs/experiments/random-baseline-20260720.md)。
统计、产物策略和 SIGTERM 恢复实验见
[`docs/experiments/persistence-metrics-20260721.md`](docs/experiments/persistence-metrics-20260721.md)。
唯一性统计、周期种子注入和跨 checkpoint 调度实验见
[`docs/experiments/novelty-reseed-20260721.md`](docs/experiments/novelty-reseed-20260721.md)。
失败分类、紧凑 checkpoint v3 和中断恢复实验见
[`docs/experiments/failure-checkpoint-v3-20260721.md`](docs/experiments/failure-checkpoint-v3-20260721.md)。
追加式 Run Summary、checkpoint v5 和 TLC 性能统计实验见
[`docs/experiments/checkpoint-v5-tlc-metrics-20260721.md`](docs/experiments/checkpoint-v5-tlc-metrics-20260721.md)。
有界反馈队列、checkpoint v6、proposal-drop 修复、动作分布和确定性恢复实验见
[`docs/experiments/checkpoint-v6-feedback-20260721.md`](docs/experiments/checkpoint-v6-feedback-20260721.md)。
balanced lifecycle、严格 Corpus 准入、Raft 语义覆盖和 checkpoint v7 的逐项对照见
[`docs/experiments/feedback-tuning-v7-20260722.md`](docs/experiments/feedback-tuning-v7-20260722.md)。
按需 TLC Action、10/10 JVM 内存与等价性验证见
[`docs/experiments/lazy-tlc-actions-20260721.md`](docs/experiments/lazy-tlc-actions-20260721.md)。
Snapshot/日志压缩、MsgSnap 受控投递、Oracle 前缀恢复和 checkpoint 确定性实验见
[`docs/experiments/snapshot-compaction-20260721.md`](docs/experiments/snapshot-compaction-20260721.md)。
网络分区/合并、三节点收敛、五节点随机 smoke 和确定性 Replay 见
[`docs/experiments/network-partition-20260722.md`](docs/experiments/network-partition-20260722.md)。
定向 partition/compaction/snapshot、retain 回归和失败 Plan 缩减见
[`docs/experiments/directed-snapshot-minimization-20260722.md`](docs/experiments/directed-snapshot-minimization-20260722.md)。
五节点 `n/3+1` 选举 quorum mutant 的最短反例、100-seed 对照、下游 snapshot panic
和统计口径见
[`docs/experiments/quorum-one-third-mutant-20260721.md`](docs/experiments/quorum-one-third-mutant-20260721.md)。
自有严格 TLC 服务迁移、模型 invariant、兼容性及性能对照见
[`docs/experiments/strict-tlc-migration-20260721.md`](docs/experiments/strict-tlc-migration-20260721.md)。
DeepSeek 官方接口核对、付费统计修复和接入前检查见
[`docs/experiments/deepseek-readiness-20260721.md`](docs/experiments/deepseek-readiness-20260721.md)。

## 验证

```bash
gofmt -w internal cmd
go mod tidy
go test ./...
go test -race ./...
go vet ./...
```

详细结构和后续模块见 [`docs/project-structure.md`](docs/project-structure.md)，
时间与超时语义见 [`docs/timer-design.md`](docs/timer-design.md)。
