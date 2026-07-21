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
- `internal/corpus`：只保留触发全局新模型状态的 Plan 和紧凑 Concrete ActionSequence。
- `internal/mutation`：Corpus Plan 的本地随机变异和可选 LLM 变异。
- `internal/llm`：厂商无关 JSON 补全接口及多 provider OpenAI-compatible 客户端。
- `internal/experiment`：候选执行、覆盖反馈、Corpus 保留和异步变异闭环。
- `internal/metrics`：协议无关的执行明细、耗时、吞吐和覆盖增长统计。
- `internal/persistence`：原子 JSON、追加式 JSONL 和崩溃尾记录修复。
- `internal/adapters/etcdraft`：etcd-raft 3.7 的最小集群适配器。
- `internal/model`：Concrete Transition 到模型事件的映射及 TLC 客户端。
- `internal/oracle`：对真实 Concrete Transition 执行基础在线安全性检查。
- `cmd/modelfuzz-ng`：读取配置和 Plan、执行轨迹并保存产物的命令行入口。
- `models/raft`：首版轻量 Raft TLA+ 模型。
- `docs`：Timer 设计与目标目录结构。

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
  --config models/raft/raft.cfg \
  --port 2023
```

首次运行会下载并校验官方 TLA+ Tools v1.8.0，再编译 NG 自己维护的 Java 服务层；
不再依赖原 ModelFuzz artifact。该服务严格拒绝无法映射、当前状态下不可执行、产生
多个后继或违反模型 invariant 的事件，不会把它们静默当作 stutter。服务启动后，
CLI 增加：

```bash
-tlc http://127.0.0.1:2023
```

每次运行必须使用一个尚不存在的输出目录，CLI 不会覆盖旧轨迹。目录中包含
解析结果、Concrete Action、Trace、模型事件、模型状态、Oracle Finding、`failure.json`
以及汇总结果。成功时 `failure.json` 为 `null`；同步 Adapter/SUT 调用发生
panic 时，其中保存失败操作、逻辑时间、失败 Action、执行前 Observation、panic 值
和 goroutine 堆栈。失败 Action 不会被写成虚假的完整 Step，已成功的 Trace 前缀仍会持久化。
当前轻量 Raft 模型已支持 crash/restart；snapshot 和 membership change 仍未
建模，Profile 会在修改真实 SUT 前拒绝可提前判断的不支持动作。

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
| snapshot/membership change | Adapter 有部分处理 | 不支持 | 需要扩展独立模型 Profile |
| PreVote/CheckQuorum | 当前关闭 | 不支持 | 启用 Raft 配置前必须先补模型 |

Raft Observation 额外暴露 `committed_prefix_available` 和
`committed_prefix_digests`。摘要以确定性 protobuf 编码计算，并只携带当前集群
commit 索引所需的检查点，避免长日志在每个 Observation 中全量展开。基础
Raft Oracle 因此可以比较两节点 `min(commitA, commitB)` 处的共同已提交前缀，
不再受未提交尾部影响，也会检查 crashed 节点保留的稳定日志。

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
| `MsgProp` | Follower 转发 proposal 时进入受控网络；投递到当前 Leader 才映射为 `ClientRequest` |
| `MsgSnap`、`MsgTimeoutNow`、`MsgPreVote` 等其他网络消息 | 不支持并返回错误，不会静默忽略 |
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
MessageID；某次成功执行只有在 controlled TLC 返回至少一个全局未见的 `State.Key`
时，才会携带 Plan 和实际 Concrete ActionSequence 进入 Corpus。完整 Trace 由逐运行
产物策略单独保存，不再重复写入 Corpus 和 checkpoint。每个新状态默认生成两个本地随机变异，
候选按 FIFO 继续执行。变异在独立 goroutine 中产生，可以和已经排队的 Plan 执行重叠。
离线随机 Mutation 还会主动用 `crash(node) ... restart(node)` 包围一段已有动作。
候选入队前会检查节点生命周期和同时停止节点上限，不会生成重复 crash 或未 crash
就 restart 的配对。
在线随机策略会根据最新节点状态生成 crash/restart，默认同时最多停止1个节点且不会
停止最后一个运行节点；发往停止节点的在途消息可在恢复后继续参与调度。
它也会向已知当前 Leader 的 Follower 生成客户端请求，并把产生的 `MsgProp` 作为
普通受控消息调度；Candidate 或尚不知道 Leader 的 Follower 不进入随机请求候选集，
避免把预算浪费在确定会被丢弃的 proposal 上。

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

- `corpus.json`：全局覆盖键及被保留的 Plan/Concrete ActionSequence；
- `experiment-settings.json`：初始化、变异和 LLM provider 的非敏感配置；
- `llm-stats.json`：启用 LLM 时的调用、失败、累计时延和 token 统计，并通过
  `by_purpose.initial`/`by_purpose.mutation` 分开记录两条路径；该文件随 checkpoint
  更新，恢复后继续累计而不是覆盖；
- `experiment-report.json`：每次执行的候选父子关系、覆盖增量和闭环汇总；
- `experiment-metrics.json`：Action、Effect、出站消息类型、解析结果及稳定原因码、模型事件、Oracle、失败、
  timer、终止原因、耗时分位数、吞吐、队列峰值和覆盖增长曲线；同时统计唯一 Plan、
  唯一 Concrete Trace、唯一模型状态路径、重复率、唯一性增长曲线及各候选来源的首次发现贡献；
- `progress.jsonl`：每次完成执行、完成变异和实验状态切换的 fsync 追加日志；
- `checkpoint.json`：紧凑 Corpus、报告、候选队列、运行中候选、待处理变异和随机编号的
  原子快照；只按 `checkpoint-every` 的运行完成边界写入，Mutation 完成只追加 journal，
  不再强制重写整个快照；尚未执行的 Report 槽位编码为 `{}`，不会按完整 Run 结构占空间；
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
允许确定性重跑。当前 checkpoint 格式版本为 v4；检查点包含实验配置指纹，修改
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
