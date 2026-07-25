# Storage/Snapshot strict soak、吞吐与重放实验（2026-07-22）

## 目的与口径

本轮在 snapshot-status 映射修复后验证当前基线，分为三段：

1. 三组真实 etcd-raft + strict TLC 纯随机实验；
2. 不连接 TLC 的并行高吞吐实验；
3. snapshot 安装、传输失败和 FastForward 的定向 strict 回归、逐步重放与
   checkpoint 恢复。

随机组每条 300 Actions，`initial-population=runs`，`initial-policy=random`，不执行
corpus mutation。`artifact-policy=failures` 仅保留失败的完整逐运行产物，但所有 run
summary、checkpoint 和聚合报告均落盘。strict 主批次耗时约 49 分 44 秒；加上三组
校准、server 启停和监测，strict 阶段墙钟约 55 分钟。无 TLC 主批次及校准超过
30 分钟；定向回归与重放约 16 分钟。

## strict TLC 随机主实验

| 配置 | Seeds | 成功/失败 | Actions | 模型事件 | 不同原始模型状态 | 不同语义状态/转移 | 最大消息队列 | 耗时 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 3n, threshold=2, retain=0 | 2500 | 2500/0 | 750,000 | 488,767 | 256,294 | 126,038 / 268,365 | 57 | 17m08s |
| 3n, threshold=4, retain=1 | 2500 | 2500/0 | 750,000 | 480,217 | 245,074 | 129,209 / 256,418 | 55 | 16m59s |
| 5n, threshold=2, retain=0 | 1350 | 1350/0 | 405,000 | 347,654 | 181,843 | 109,820 / 202,220 | 127 | 15m38s |

三组共 6350 条独立 Plan/Trace/模型状态路径、1,905,000 Actions 和 1,316,638
模型事件。所有执行均以 `plan_action_budget` 正常结束；oracle、runtime、Mapper 和 TLC
failure 均为 0。每组的 Plan、Trace 和状态路径 digest 都是 100% 唯一。

TLC server 的 3 节点两组最终计数分别为 2500/2500 和 2500/2500 请求成功，错误码
为空。5 节点 server 未在 30 条校准后重启，因此最终 1380/1380 包含校准；主实验
本身仍由 run report 精确记录为 1350/1350。动作缓存均被限制在 16,384；3 节点两组
发生 3518/5019 次淘汰，5 节点连同校准发生 38,926 次淘汰，未出现无界增长或持续
吞吐下降。

## 随机策略下 Snapshot 自然覆盖

下表是“至少在一条 300-Action 轨迹中发生一次”的 seed 数及占比，而不是事件总数：

| 配置 | Create | Send | Deliver | Install | FastForward | reject/stale | status success | status failure | status ignored |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 3n t2/r0 | 2496 (99.84%) | 2331 (93.24%) | 2309 (92.36%) | 1937 (77.48%) | 457 (18.28%) | 1092 (43.68%) | 2092 (83.68%) | 310 (12.40%) | 1061 (42.44%) |
| 3n t4/r1 | 2473 (98.92%) | 1847 (73.88%) | 1811 (72.44%) | 1352 (54.08%) | 261 (10.44%) | 672 (26.88%) | 1497 (59.88%) | 163 (6.52%) | 689 (27.56%) |
| 5n t2/r0 | 1258 (93.19%) | 1110 (82.22%) | 1050 (77.78%) | 724 (53.63%) | 293 (21.70%) | 637 (47.19%) | 880 (65.19%) | 117 (8.67%) | 625 (46.30%) |

对应事件总量为 34,866 次 snapshot 创建、10,716 次发送、10,245 次投递、5851 次
安装、1132 次 FastForward、3262 次 reject/stale，以及 7349/629/3125 次
success/failure/ignored status。结论是：这些路径不需要内部状态注入就能被普通随机计划
自然覆盖；其中 FastForward 和 status failure 概率较低，但在 1000 级 seed 规模已足够
稳定出现。t4/r1 相比 t2/r0 明显降低创建和传输频率，符合阈值增大及保留一条日志的
预期。

## 无 TLC 高吞吐

| 配置 | 完成轨迹 | 成功/失败 | Actions | concrete 模型事件 | 最大消息队列 | 平均 Actions/s | 墙钟 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 3n, 300 Actions | 6116 | 6116/0 | 1,834,800 | 1,196,827 | 59 | 2034 | 15m02s |
| 3n, 1000 Actions | 4440 | 4440/0 | 4,440,000 | 1,161,765 | 75 | 4487 | 16m29s |

两组共 10,556 条轨迹、6,274,800 Actions 和 2,358,592 concrete 模型事件，失败为
0。两组都由 `parallelism=4` 执行，涵盖 crash/restart、partition/heal、drop、duplicate
和 request。第二组在首次到达 4318 条后中断，随后从 checkpoint 连续恢复到 4440；
index 0..4439 和 seed 800000..804439 均无缺失或重复。

无 TLC 时没有模型状态，因此 corpus 按设计为空，`unique_model_states=0`。这一阶段只能
证明 concrete runtime 和资源稳定性，不能作为 TLC 模型覆盖结论。

### 发现：ready queue 耗尽后的并行度退化

300-Action 组前 4000 条耗时约 200 秒（约 20 runs/s），4000 到 6000 条耗时约
659 秒（约 3 runs/s）；1000-Action 组在 4000 条后也出现相同趋势。转折点与
`max_ready_candidates=4096` 精确重合。

原因位于 feedback runner：初始候选超过上限时只保留 4096 条；队列耗尽后，仅当
`running==0` 才补充 1 个随机 seed。于是四个 worker 每轮先形成全局 barrier，再只启动
一条执行，实际退化为近似串行。这不是 Raft、TLC、GC 或磁盘容量问题；RSS 有升有降，
消息队列峰值也只有 59/75。该问题不改变正确性统计，但会显著限制大规模纯随机吞吐。

### 后续修复与验证

runner 已改为在 ready queue 和 pending mutation 都为空时，按
`min(parallelism-running, runs-nextRunIndex)` 补足所有空闲槽位；有 pending mutation
时仍保持原有优先级，不会让新 seed 抢占变异预算。确定性单元测试会让四个 worker 中
三个保持运行，验证第一个空闲槽出现后立即启动补充 seed，不再依赖耗时比例判断。

相同配置的 5000-seed 修复后回归结果为 5000/5000 成功、1,500,000 Actions、零失败，
总耗时 265.4 秒：

- 0..4000：211.9 秒，约 18.88 runs/s；
- 4000..5000：53.4 秒，约 18.71 runs/s；
- 跨过 4096 后吞吐变化约 0.9%，旧实现同区段仅约 3.17 runs/s；
- 与旧实现的 4000..5000 区段相比提升约 5.9 倍。

产物位于 `runs/soak-20260722/notlc-refill-fix-3n-300a/`。

## 定向 strict 回归与重放

定向回归覆盖 3/5 节点的三类场景：

| 场景 | 3n 成功/失败 | 5n 成功/失败 | 每条必须命中的边界 |
|---|---:|---:|---|
| snapshot-partition | 1100/0 | 550/0 | snapshot install、随后 stale/reject、status success/ignored |
| snapshot-failure | 1100/0 | 550/0 | status failure、heartbeat 后 retry、install、status success |
| snapshot-fast-forward | 1100/0 | 550/0 | 自然 FastForward、status success，不执行 Restore |

共 4950/4950 条 strict 定向轨迹通过。当前版本另外生成 3n/5n、三场景各 3 条完整
artifact，并将每条 trace 严格重放两次：36/36 完成；3 节点逐步匹配 23/24/31 步，
5 节点逐步匹配 38/39/52 步。

旧 phase3 的 5 节点 install trace 在第 34 步按预期分歧：历史 effect 在成功投递
`MsgSnap` 后没有 `raft.snapshot_status_reported`，当前版本新增了真实
`ReportSnapshot(SnapshotFinish)` 映射。这是 trace 语义版本演进，不是当前版本的
非确定性；当前版本自产生、自重放全部一致。

## 其他观察与结论

- 运行中读取 `/metrics` 时曾短暂看到 `requests=succeeded+1`，下一次读取恢复一致，且
  `errors_by_code`、run report 和失败 artifact 均为空。说明当前 `failed` 字段实际上会
  把 in-flight 请求短暂计作失败；最终静态 metrics 可信，在线监控不应单独依据该瞬时值
  告警。
- `go test ./...`、`go vet ./...`、关键包 race test 和 strict TLC server 集成测试通过。
  完整 Storage/Snapshot 检查仍为 76 generated / 48 distinct / depth 11；聚焦 progress
  检查为 30 generated / 9 distinct / depth 6，均无错误。
- 本轮没有复现 snapshot-status 映射不一致、模型 invariant violation、oracle failure、
  panic、消息队列上限或 checkpoint 损坏。当前 Storage/Snapshot 基线可以认为适合继续
  扩展 TLA+ 或开展更长实验。
- 这轮结论是轨迹/模型状态与 snapshot 语义覆盖，不是 Go 源码行覆盖率结论；动态
  membership、PreVote、损坏/分块 snapshot 仍未因此获得覆盖。

## 产物

原始产物位于 `runs/soak-20260722/`，约 445 MiB，包括所有聚合报告、run summaries、
checkpoint、TLC server metrics、当前版本重放源和重放结果。未提交、未推送，也没有
清理任何既有实验产物。
