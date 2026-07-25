# v1 长时间随机基线、回归与重放（2026-07-23 至 2026-07-24）

## 结论摘要

正式 v1 的六组纯随机基线共完成 1,473,294 条 300-Action 轨迹、441,988,200 个 Action、583,288,372 个 Effect 和 307,827,276 个模型事件。最终运行记录全部成功，没有 runtime、Mapper、strict TLC、Oracle 或 SUT failure；所有轨迹都以 `plan_action_budget` 正常结束。三组 strict TLC 共得到 7,146,210 个不同原始模型状态、5,364,644 个不同语义状态和 7,924,417 个不同语义转移。

Snapshot create、send、install、FastForward、reject/stale 和三类 `MsgSnapStatus` 都能被纯随机 300-Action Plan 自然触发。三组 strict 与三组无 TLC 实验使用不同连续 seed 区间，但对应 Snapshot 行为的逐轨迹命中率通常只相差 0.0–0.5 个百分点，说明这些概率在当前随机策略和轨迹长度下已经相当稳定，也说明 strict TLC 观察路径没有明显改变 concrete etcd-raft 行为。

长跑结束后又执行了 4,950 条定向 strict TLC 回归、21 条带完整 artifact 的 strict TLC 样本和 42 次严格 Replay。批量回归、代表样本和 Replay 全部通过；三个直接取自长跑 seed 区间的样本，其 Action、Effect、模型事件、终止原因和 Snapshot 指标与原始 run summary 逐字段一致。

这批结果支持“当前 v1 基线适合继续做缺陷注入和 LLM 对照实验”，但不证明系统无 bug，也不覆盖动态 membership、PreVote、真实磁盘/WAL 故障、外部进程 backend 或超过 300 Action 的单条历史。无 TLC 组不产生 TLC 状态，不能用于模型状态覆盖结论。

## 实验口径

随机基线使用正式版本 `v1.0.0`、Trace schema 1、checkpoint schema 1 和 `raft-coverage-v1`。每条轨迹最多执行 300 个 Action，初始化策略为 `random`，`initial_population=1`，每完成一条运行补充一个新的连续随机 seed，`min_new_model_states=1000000`，因此 Corpus 始终为空且不执行 mutation。`artifact_policy=failures` 只为失败保存完整逐运行 artifact，但所有成功运行仍保存 run summary、Plan/Trace digest、聚合报告和 checkpoint。

三组 strict TLC 串行执行，每组约 2 小时；三组无 TLC 实验使用四个 worker，每组约 8 小时。seed 分别从 1,000,000、2,000,000、3,000,000、4,000,000、5,000,000 和 6,000,000 开始。六组的 run index 都从 0 连续到 `runs-1`，`seed=base_seed+index` 的不匹配数为 0，index 的数量、范围与求和校验全部通过。

## 六组最终结果

| 组 | Runs | Actions | Effects | 模型事件 | 原始状态 | 语义状态 | 语义转移 | Actions/s | 最大消息队列 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| strict 3n, t2/r0 | 19,112 | 5,733,600 | 7,123,571 | 3,748,300 | 1,905,832 | 1,387,750 | 2,028,561 | 795.5 | 59 |
| strict 3n, t4/r1 | 21,285 | 6,385,500 | 7,777,903 | 4,082,096 | 2,014,098 | 1,400,788 | 2,113,125 | 886.5 | 64 |
| strict 5n, t2/r0 | 24,344 | 7,303,200 | 11,802,498 | 6,294,948 | 3,226,280 | 2,576,106 | 3,782,731 | 1,013.5 | 158 |
| no-TLC 3n, t2/r0 | 541,348 | 162,404,400 | 201,614,476 | 106,145,555 | — | — | — | 5,638.1 | 78 |
| no-TLC 3n, t4/r1 | 547,923 | 164,376,900 | 200,249,449 | 105,091,596 | — | — | — | 5,706.6 | 81 |
| no-TLC 5n, t2/r0 | 319,282 | 95,784,600 | 154,720,475 | 82,464,781 | — | — | — | 3,325.7 | 169 |

六组均满足 `succeeded=runs`、`failed=0`、`failure_counts={}`、`oracle_counts={}`。每组的唯一 Plan 数和唯一 concrete Trace 数都等于 runs；三组 strict 的唯一模型状态路径数也都等于 runs。这里的“唯一 Trace”是 digest 唯一，不表示 `artifact_policy=failures` 保存了每条成功轨迹的完整 `trace.json`。

三组 strict 共完成 64,741 条轨迹和 19,422,300 个 Action；三组无 TLC 共完成 1,408,553 条轨迹和 422,565,900 个 Action。若仅把每条轨迹粗略视为独立同分布样本，0 次失败对应的“rule of three”95% 上界约为 strict 每轨迹 `4.6e-5`、全部轨迹每轨迹 `2.0e-6`。连续 seed、不同配置和固定 300-Action horizon 并不严格满足该统计假设，因此这只能作为量级提示，不能解释成 failure 概率为零。

## strict TLC 覆盖分析

语义投影把三节点 t2/r0、三节点 t4/r1 和五节点 t2/r0 的原始状态分别压缩到 72.8%、69.5% 和 79.8%。这说明投影确实合并了部分不会改变未来协议行为的细节，同时仍保留了大量由选举、日志、提交和 Snapshot 边界产生的差异。该比例不是“冗余状态百分比”的严格证明，因为原始状态与语义状态不是简单的一对一分类。

覆盖增长没有在 2 小时内明显饱和。把每组按完成 runs 分成四等份，最后四分之一相对第一四分之一仍产生：

| 组 | 最后一季度/第一季度原始状态增量 | 语义状态增量 | 语义转移增量 |
|---|---:|---:|---:|
| strict 3n, t2/r0 | 95.6% | 91.3% | 92.5% |
| strict 3n, t4/r1 | 97.3% | 91.9% | 92.7% |
| strict 5n, t2/r0 | 98.3% | 96.2% | 96.8% |

因此更长实验仍会持续增加精确状态和语义状态/转移。不过接近线性的状态增长也可能包含值、term、日志内容组合的持续变化，不能单独证明新的高层协议行为也按同样速度增长。后续比较随机方法、mutation 和 LLM 时，应同时报告语义状态、语义转移、行为事件命中以及缺陷发现，而不能只比较状态总数。

五节点组每 100 Action 的语义新颖性为 87.1，高于两个三节点组的 59.6 和 55.0；五节点也产生更多不同状态与转移。这符合节点组合和 Leader progress 维度增加后的状态空间扩张。五节点 strict 的 Actions/s 反而更高，不能解释为五节点执行成本更低，因为在线随机策略在不同节点数下产生的 Action 类型分布明显不同，且本实验不是固定 Plan 的节点规模微基准。

## Snapshot 自然覆盖

下表统计“至少在一条轨迹中发生一次”的 run 数和占比，列顺序为 create、send、deliver、install、FastForward、reject/stale、status success、status failure 和 status ignored。

| 组 | Create | Send | Deliver | Install | FastForward | Reject/stale | Status success | Status failure | Status ignored |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| strict 3n t2/r0 | 19,071 (99.8%) | 17,766 (93.0%) | 17,562 (91.9%) | 14,860 (77.8%) | 3,638 (19.0%) | 8,481 (44.4%) | 16,241 (85.0%) | 2,214 (11.6%) | 8,166 (42.7%) |
| strict 3n t4/r1 | 21,009 (98.7%) | 15,793 (74.2%) | 15,457 (72.6%) | 11,549 (54.3%) | 2,256 (10.6%) | 5,807 (27.3%) | 13,016 (61.2%) | 1,256 (5.9%) | 5,917 (27.8%) |
| strict 5n t2/r0 | 22,577 (92.7%) | 20,036 (82.3%) | 19,107 (78.5%) | 12,885 (52.9%) | 5,377 (22.1%) | 12,110 (49.7%) | 15,762 (64.7%) | 2,210 (9.1%) | 11,790 (48.4%) |
| no-TLC 3n t2/r0 | 540,210 (99.8%) | 503,825 (93.1%) | 498,271 (92.0%) | 423,210 (78.2%) | 101,319 (18.7%) | 240,451 (44.4%) | 458,779 (84.7%) | 62,768 (11.6%) | 231,781 (42.8%) |
| no-TLC 3n t4/r1 | 541,575 (98.8%) | 408,044 (74.5%) | 399,495 (72.9%) | 298,615 (54.5%) | 57,472 (10.5%) | 151,029 (27.6%) | 335,526 (61.2%) | 33,396 (6.1%) | 153,754 (28.1%) |
| no-TLC 5n t2/r0 | 296,836 (93.0%) | 264,053 (82.7%) | 251,915 (78.9%) | 170,385 (53.4%) | 70,356 (22.0%) | 160,194 (50.2%) | 208,134 (65.2%) | 28,899 (9.1%) | 155,307 (48.6%) |

六组合计自然产生 8,068,821 次 snapshot create、2,530,933 次 send、1,366,954 次 install、268,253 次 FastForward 和 815,820 次 reject/stale。FastForward 与 status failure 虽然比 create/send 少，但最低逐轨迹命中率仍分别为 10.5% 和 5.9%，在当前规模下不属于“只能靠手工注入才能出现”的路径。

三节点 t4/r1 相比 t2/r0 的 create 逐轨迹命中率只小幅下降，但单 Action 的 create 事件率由约 2.29% 降到 1.34%；send、install、FastForward 和 status failure 的逐轨迹命中率则明显下降。这符合更高 snapshot threshold 和保留一条日志缩小 Snapshot 传输窗口的预期。由于 threshold 和 retain 在两组之间同时变化，本实验不能把差异单独归因于其中一个参数。

strict 与无 TLC 的相同配置没有使用相同 seed 区间，但 Snapshot 命中率高度一致。例如三节点 t2/r0 的 FastForward 为 19.0% 对 18.7%、status failure 均为 11.6%；五节点 t2/r0 的 FastForward 为 22.1% 对 22.0%、status failure 均为 9.1%。这是一项很强的内部一致性检查，但仍不是同 seed A/B 性能实验。

## 吞吐与稳定性

三节点无 TLC 的吞吐约为 5,638–5,707 Actions/s，五节点为 3,326 Actions/s。五节点相对三节点 t2/r0 低约 41%，符合更多节点、更多消息和更大的队列带来的 concrete 执行成本。无 TLC 与 strict 的吞吐倍率约为 7.1、6.4 和 3.3 倍，但该倍率同时混合了“是否调用 TLC”和“parallelism=1 对 4”两个变量，不能直接作为 TLC 单次调用开销。

长跑修复后没有新的 OOM。最终实验目录约 5.0 GiB，所有 checkpoint、report 和 run summary 都能读取，六组 index/seed 水位连续。无 TLC 组的模型状态和模型状态路径为 0 是设计结果：没有 TLC 响应就没有可统计的模型状态，不能把它理解为覆盖缺失或执行失败。

### 长跑中的 runner OOM 与二进制边界

第一组 strict 在首次运行到 checkpoint 3000 后，由于 feedback runner 按 `runs=10,000,000` 预分配多个 channel 和 report slice，被内核 OOM killer 终止。修复后从 checkpoint 3000 恢复并完成目标时长；修复内容是把 channel 和 coverage timeline 预分配改为有界容量，不改变 Action 生成、etcd-raft、模型映射或 Oracle 语义。后续没有再发生 OOM。

因此六组结果是可信的功能与恢复基线，但不是严格的“同一个二进制从头运行到底”发布基线：第一组前 3000 条使用初始二进制，剩余部分使用第一轮内存修复；第二组在第二项 report 预分配修复前已经启动；第三组 strict 和三组无 TLC 使用最终修复二进制。第一组初始 TLC server 的 3000 次请求 metrics 没有在重启时保留，恢复段静态 metrics 为 16,112/16,112；另外两组 strict server metrics 分别为 21,285/21,285 和 24,344/24,344，错误码均为空。若论文或发布材料要求单二进制可审计性，应重跑三组 strict；不需要因此重跑 24 小时无 TLC 稳定性组。

## 回归与重放

长跑完成后执行了与 pre-v1 soak 相同规模的 4,950 条定向 strict TLC 回归：

| 场景 | 3 节点 | 5 节点 | 必须命中的边界 |
|---|---:|---:|---|
| snapshot-partition | 1,100/1,100 | 550/550 | install、reject/stale、status success/ignored、`policy_complete` |
| snapshot-failure | 1,100/1,100 | 550/550 | status failure、retry、install、status success、`policy_complete` |
| snapshot-fast-forward | 1,100/1,100 | 550/550 | FastForward、status success、不执行 Restore、`policy_complete` |

批量回归共执行 156,750 个 Action 和 202,950 个模型事件。3 节点 TLC server 为 3,300/3,300 请求成功，5 节点为 1,650/1,650，请求错误码为空。逐条 run summary 校验确认每条轨迹都命中对应边界，而不是只依赖聚合事件总数。

另外生成了 3/5 节点、三类定向场景各 3 条完整 artifact，共 18 条；再从三组 strict 长跑各选一条同时包含 FastForward 和 status failure 的随机 seed：

- `strict-3n-t2-r0`: seed 1,000,007；
- `strict-3n-t4-r1`: seed 2,000,578；
- `strict-5n-t2-r0`: seed 3,000,116。

三个随机 seed 重新生成后的 Action、Effect、模型事件、终止原因和完整 metrics 与长跑 `runs.jsonl` 中的记录逐字段相等。21 条完整 Trace 每条重放两次，共 42/42 次 `status=completed`，累计匹配 3,042 个步骤，单次最短 23 步、最长 300 步，没有 ObservationDigest、MessageID、Effect 或节点状态分歧。

代码回归同时通过 `go test ./...`、`go vet ./...`、`golangci-lint run ./...`，以及 `internal/experiment`、`internal/runtime`、`internal/trace` 和 `internal/adapters/etcdraft` 的 race test。strict TLC server 集成测试通过；Storage/Snapshot 完整模型检查为 76 generated / 48 distinct / depth 11，progress 聚焦检查为 30 generated / 9 distinct / depth 6，均无错误。

## 能说明什么，不能说明什么

当前结果能够说明：

- runner 内存修复后可以持续运行 30 小时级串行实验套件，不再随高 `runs` 上限预分配巨量内存；
- 纯随机策略能自然覆盖当前模型中的核心 Raft、故障、恢复和 Snapshot 生命周期；
- Snapshot status 映射在大规模随机 strict、定向 failure/retry、FastForward 和严格 Replay 中保持一致；
- semantic coverage 在长跑中持续增长，当前 2 小时 strict 基线尚未饱和；
- 300-Action 范围内没有发现当前 Oracle、TLC 模型或 runtime 能识别的安全性失败。

当前结果不能说明：

- etcd-raft 或 ModelFuzz-NG 已经“没有 bug”；
- 未建模或未检查的性质也正确；
- 动态 membership、PreVote、真实磁盘恢复、损坏/分块 Snapshot 已被覆盖；
- 无 TLC 的 1,408,553 条轨迹提供了 TLA+ 状态覆盖；
- 精确状态数等价于源码行覆盖率或高层行为覆盖率；
- 原 ModelFuzz 的 missing-snapshot 报告已经被证明为误判。该结论仍需要正常 quorum、正确持久化契约和合法 snapshot/compaction 顺序下的专门 A/B 缺陷实验。

## 产物与复核入口

长跑原始产物位于 `runs/v1-random-baseline-20260723/`，回归和重放位于其 `regression-replay/` 子目录。长跑产物约 5.0 GiB，回归与重放约 184 MiB。关键入口包括每组的 `experiment-report.json`、`experiment-metrics.json`、`checkpoint.json`、`runs.jsonl`，以及回归目录的 `status.log`、`bulk/status.log`、TLC server metrics、完整 `trace.json` 和 `replay-result.json`。这些运行目录按仓库约定不提交 Git。

本报告对应基线提交 `e65270462acf9fb0a4aeacfc4a7dfc7d78961c0d`，同时包含尚未提交的 runner 有界预分配修复。长跑最终使用的 Go 二进制 SHA-256 为 `26886cf0d2e6e2976730bd5edb62656e27dcab0c11710d4187e982153c324430`；回归阶段从当前工作树重新构建的二进制 SHA-256 为 `782b7e327c01ee988972c6cbb0a599052981c5dcfef54141c2f003101fdd7658`。
