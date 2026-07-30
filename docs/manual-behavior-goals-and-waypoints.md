# 人工 Behavior Goal 与 Waypoint Frontier（第三轮）

日期：2026-07-28
Schema：`raft-behavior-goals-v1-prototype`

> 本文保留第三轮的历史设计与 3-seed 结果。10-seed 稳定性、hint/top-K/prefix/
> Distance 消融、directed 参考和匹配 mutant 实验见
> [第四轮 Waypoint Frontier 验证与缺陷检出](waypoint-frontier-validation-and-bug-detection.md)；
> 第四轮结果优先，不应把本文“尚未运行”的条目当作当前状态。

## 1. 本轮目标与结论

本轮没有接入 LLM，而是先验证三个基础问题：

1. 人工定义的分布式行为目标能否在线、因果地判断；
2. 最有进展的执行前缀能否稳定重放，并继续变异后缀；
3. Waypoint Frontier 是否有独立于“提高相关 Action 概率”的收益。

实现结果是：两个固定 Raft Goal 都可在线求值、离线重算和确定性重放。在
3 个 seed 的 strict TLC 小实验中，Frontier 对两个 Goal 都达到 3/3；普通变异
都是 0/3。Goal-aware-only 对 Goal A 为 0/3、对 Goal B 为 3/3。

这说明：

- Goal A 的本次结果包含 Frontier 的独立收益信号；
- Goal B 的“能否到达”主要可由人工 operator hints 解释，Frontier 只把平均
  Concrete Action 从 80 降到 40；
- 3 个 seed 太少，而且当前 operator 高度确定性，不能据此宣称普遍优越。

因此下一轮建议选择方向 A：继续扩充 seed、降低 operator/evaluator 耦合并修正
人工 Goal/Distance；本轮不进入 LLM 阶段。

## 2. 为什么暂不接入 LLM

如果 Goal、Waypoint、Distance 或 replay 本身不稳定，LLM 只能放大噪声，无法判断
收益来自语言模型、人工 operator，还是前缀复用。本轮先把可重复的非 LLM 基线做实。
实现中没有真实或模拟 LLM 请求，所有实验 `llm_calls=0`。

后续 LLM 应解决纯代码困难的问题，例如根据复杂失败历史提出新的局部测试策略；
不应替代合法 Action 检查、数值打分、MessageID 查找或确定性 replay。

## 3. 已实现能力

### 3.1 Behavior Goal schema

`BehaviorGoalDefinition` 是静态、版本化定义，包含：

- `goal_id`、名称和说明；
- 支持节点数与配置约束；
- 有序 Waypoint；
- entry/target predicate；
- 允许的 Action 类型；
- 人工 mutation hints 与 forbidden patterns；
- 默认候选、Action、Waypoint 预算；
- 成功所需 evidence。

定义序列化稳定，Goal ID 唯一。未知 Goal 和不支持的节点数会明确报错。
当前两个 Goal 要求 3 或 5 节点及 `storage-snapshot` 模型 profile。

### 3.2 Goal Instance schema

`GoalInstance` 保存一次具体执行的动态信息：

- Goal ID 和实例 ID；
- `Leader`、`TargetFollower` 的稳定绑定；
- 当前 Waypoint；
- 每个 Waypoint 的 reached/current-satisfied、首次到达位置、Distance 和 evidence；
- Event evidence、相关 MessageID、Facet/Interaction 摘要；
- CompletedWaypointCount、当前 Distance、最后进展位置、前缀长度；
- 预算使用、invalid reason 和 stable key。

静态定义与动态实例分离，避免把某次执行的节点或 MessageID 写回 Goal 定义。

### 3.3 Waypoint schema

Waypoint 有两类：

- `state`：当前状态满足谓词。Reached 为历史事实，`current_satisfied` 另行记录，
  因此 Leader 后续变化不会抹掉已发生的阶段；
- `event_evidence`：必须观察到真实 Action、Effect、映射事件或精确 MessageID，
  不能从最终状态反推早期事件。

字段包含稳定 ID、类型、注册谓词、是否 sticky、局部预算和中文说明。不支持自由文本
表达式解释器，谓词均为审查过的 Go 实现。

## 4. 符号节点绑定

W1 要求唯一 active Leader 且 Leader 所在连通分量有 quorum。`Leader` 绑定该节点。
`TargetFollower` 在 active 非 Leader 节点中选择：

1. `last_index` 最小者；
2. 相同时选择 NodeID 最大者。

绑定只创建一次，不依赖 map 遍历；后续不会静默换节点。绑定节点失效会得到
not-decidable 或 invalid reason，而不是重新解释之前的 evidence。节点整体置换后，
完成阶段和 Distance 语义保持一致，但具体绑定 ID 按新拓扑变化。

## 5. Prefix causality

Engine 仅在显式 `RunObserved` 时，在一条 Concrete Action 已完成且 Mapper 已产生
该步事件后，把以下只读前缀交给 evaluator：

- PlanAction/Concrete Action 下标；
- 该步 before/after Observation；
- StepRecord 与 Effects；
- 该步实际映射出的所有模型事件。

Evaluator 自己从初始 Observation、Action 和 Effects 重建队列、分区、duplicate
MessageID 和恢复上下文。它不读取未来 Trace 或最终状态：

- Snapshot 安装只能在对应投递步出现 `raft.snapshot_applied` 后成立；
- higher-term 成功必须投递 W5 绑定的精确 MessageID；
- stale/same-term 消息、`MsgProp` 和客户端 Request 不算 higher-term evidence；
- Restart 后如果已经完成追赶，再投递消息会明确判为时序无效；
- 大 lag 本身不等于 snapshot-required，必须有 `next < first`、
  `pending_snapshot`、`StateSnapshot` 或真实 MsgSnap。

一个 Concrete Action 映射多个事件时按 Mapper 顺序逐个求值；零事件 Action 仍产生
一个 stutter frame，以保存 crash/heal 等 Action evidence。

## 6. 在线与离线对齐

在线 evaluator 使用上述 PrefixObserver。离线 `Recompute` 从以下 artifact 重建同一
因果序列：

- initial Observation；
- Trace；
- Plan resolutions；
- persisted model events；
- 同一 Mapper 配置。

离线首先重新映射每个 Step，并逐事件与持久化事件比较；再使用与在线完全相同的
evaluator。对齐比较包含 binding、progress、FirstReachedStep、Distance、
MessageID evidence 和 target reached。

注意：Engine 的 strict TLC 仍在整条 Concrete Trace 完成后批量执行，因此在线 Goal
谓词使用 Runtime Observation、Effect 和 Mapper event，不读取“未来 TLC state”。
TLC 状态、TLC 失败和 Goal progress 分开持久化。这是当前代码真实边界。

本轮 195 个候选执行的 online/offline mismatch 总数为 0。

## 7. Progress、Distance 与排序

Progress 使用词典序：

1. CompletedWaypointCount 更多；
2. 当前 Waypoint Distance 更小；
3. evidence 更完整；
4. replay prefix 更短；
5. stable key，用于完全相同时的确定性排序。

不使用墙钟时间排序。Distance 是每个注册谓词的离散、可解释进度，不是假装成全局
连续度量；0 只表示谓词已经满足。例子：

- election：无 Leader、Candidate、唯一 Leader 但无连通 quorum、满足；
- lag：none/one/small/large，并额外检查多数派 commit 与 committed prefix；
- snapshot delivery：普通复制消息、MsgAppResp rejection、MsgSnap pending、精确投递；
- term advance：no gap、存在 gap 但缺事件证据、真实 term advance；
- higher-term：无目标协议消息、stale/same、higher pending、精确投递。

Progress novelty 与“每条 Trace 内发生过 progress update”不同。实验中的
Goal/Facet 关系使用跨候选全局最佳值：只有超过此前最佳 Waypoint，或在同一阶段降低
Distance，才算新 Goal progress。

## 8. Frontier top-K、去重与多样性

`FrontierSeed` 保存：

- Goal/Waypoint/progress/bindings；
- 截到最后一次真实 progress 的 Plan prefix；
- 同边界的精确 Concrete Trace prefix；
- prefix Observation；
- runtime seed、ExecutionID、parent 和 replay 结果；
- 语义去重键。

每个 Waypoint 最多保留 top-K。语义键保留绑定角色、粗粒度 term/log/storage
位置、分区和按链路/消息类型聚合的队列形状，但不包含每个新 MessageID；MessageID
只用于因果 evidence。相同语义形状不会无限保存，更好 progress 可以替换较差项。

选择只在最高已到达 Waypoint 的 top-K 内确定性轮转，避免“最短但相同进展”的前缀
长期饿死其他因果形状。本轮 top-K=6，没有发生 eviction；插入和多样性机制已经测试，
但实际 top-K 压力仍需更长实验。

## 9. Prefix preservation 与 replay

Frontier 模式只保留 `PrefixEndActionIndex` 之前的 PlanAction，并在其后附加一个局部
operator。执行新候选前，用相同 ExecutionID、runtime seed 和 Runtime 配置重放精确
Trace prefix，逐步比较时间、Action、Effect、节点状态和 Observation digest。

replay 不一致会拒绝该候选，并保存原因，不会继续在错误前缀上变异。普通变异和
Goal-aware-only 不锁定 prefix，也不从 Frontier 重启。

本轮共 39 次 prefix replay，39 次成功。

## 10. Goal-aware mutation

Goal-aware mutation 只使用注册的局部 operator，不编码完整成功 Plan。典型操作包括：

- 选举阶段投递 vote 消息或 timeout 非 Leader；
- 对已绑定 TargetFollower 做 partition/crash/restart；
- 在 Leader 连通 quorum 内提交一轮 request；
- 投递 TargetFollower 的 `MsgAppResp`，使 Leader 进入 Snapshot 发送路径；
- 按精确队列位置投递 MsgSnap 或 higher-term MessageID。

每次生成仍通过 Plan 验证、Resolver、模型 profile、Runtime 和 Oracle。默认
`mutation.Random` 未修改；Frontier 独立于默认 Corpus。

## 11. Goal A：Partition 后通过 Snapshot 追赶

Goal ID：`snapshot-catchup-after-partition`

| Waypoint | 类型 | 成功条件 |
|---|---|---|
| W1 | State | 唯一 Leader、连通 quorum，绑定 TargetFollower |
| W2 | Event | 真实 Partition 隔离目标，Leader 一侧仍有 quorum |
| W3 | State | 分区后多数派 commit 推进，目标至少 small lag，committed prefix 安全 |
| W4 | State | `next < first`、pending/StateSnapshot 或 MsgSnap 证明必须 Snapshot |
| W5 | Event | 真实 Heal |
| W6 | Event | 精确发往目标的 MsgSnap MessageID 被投递 |
| W7 | Event | 同一 Snapshot 产生安装 Effect，目标存储/恢复推进且前缀安全 |

W4 明确排除了“仅仅 lag 很大”的误判。W6 的 Distance 进一步区分复制消息、
rejection 和 MsgSnap pending，用于保存产生 Snapshot 所必需的中间因果前缀。

## 12. Goal B：Restart 后处理 higher-term 消息

Goal ID：`restart-then-higher-term-message`

| Waypoint | 类型 | 成功条件 |
|---|---|---|
| W1 | State | 唯一 Leader、连通 quorum，绑定 active TargetFollower |
| W2 | Event | 真实 Crash 目标，其余 active 节点仍可形成 quorum |
| W3 | Event | 目标离线时活动集群 term 真实推进，并有 timeout/leader transition evidence |
| W4 | Event | 真实 Restart，目标 term/log/commit 尚未完成追赶 |
| W5 | State | 队列中存在发往目标的、term 更高的协议消息，绑定 MessageID |
| W6 | Event | 精确 MessageID 投递，目标 term 正确前进且无 regression |

强制时序为 Crash → term advance → Restart incomplete → higher-term pending →
exact delivery。客户端请求不能充当协议 term evidence。

## 13. 三种对照方法

1. `unguided-local-mutation`：使用现有 `mutation.Random`，Goal 只观察，不影响生成；
2. `goal-aware-operators-only`：使用相同 Goal 与局部 operator，不保留 Frontier，
   每个候选继续扩展上一条完整 Plan；
3. `waypoint-frontier`：使用 Goal-aware operator、Progress、top-K、
   prefix preservation 和 replay。

三者从同一个 goal-neutral election/request 初始 Plan 开始。Goal A 已有
snapshot-partition directed policy 可以作为未来参考上界，本轮没有把它混入主要三组
统计。

## 14. CLI

```bash
modelfuzz-ng goal-search \
  -config examples/config-snapshot.json \
  -goal snapshot-catchup-after-partition \
  -mode waypoint-frontier \
  -output /tmp/goal-run \
  -nodes 3 -seed 101 \
  -candidate-budget 15 -action-budget 1500 \
  -max-actions-per-plan 140 -per-waypoint-budget 15 \
  -frontier-top-k 6 \
  -strict-tlc=true -tlc http://127.0.0.1:22041 \
  -goal-aware-mutation=true -prefix-preservation=true \
  -save-all-runs=true \
  -snapshot-threshold 3 -retain-entries 1 \
  -crash-quota 2 -partition-enabled=true \
  -workers 1 -replay-verify=true
```

未知 Goal/mode、错误模型 profile、节点数不匹配和模式开关不一致都会报错。当前为了
反馈顺序与 replay 确定性只允许 `workers=1`；参数会保存，但并行搜索尚未实现。

对照汇总：

```bash
modelfuzz-ng goal-compare \
  -input /tmp/modelfuzz-ng-waypoint-experiments-20260728-final \
  -output /tmp/modelfuzz-ng-waypoint-experiments-20260728-final/comparison-summary.json
```

## 15. 持久化 artifact

每个独立搜索目录包含：

- `goal-definition.json`；
- `goal-settings.json`，包含所有有效配置、`llm_calls=0` 与 resume 能力；
- `goal-progress.jsonl`；
- `frontier-manifest.json`；
- `frontier-seeds/*-plan.json` 与 `*-trace.json`；
- `replay-verification/candidate-*.json`；
- `target-reached.json`（成功时）；
- `final-report.json`；
- `runs/candidate-*` 下的原标准 config、Plan、resolution、Action、Trace、
  model event/state、Runtime result、failure、Oracle finding；
- 每个 run 的 `goal-progress-online.json` 与 `goal-progress-offline.json`。

跨运行 `comparison-summary.json` 由 `goal-compare` 生成。写入使用现有原子 JSON 和
fsync JSONL。没有修改原有 experiment artifact。本轮没有实现 Goal Search
checkpoint/resume，设置与报告明确记录 `checkpoint_resume_supported=false`。

## 16. 小规模实验配置

原始 artifact：

`/tmp/modelfuzz-ng-waypoint-experiments-20260728-final`

共同配置：

- 3 节点正常 etcd-raft，无人工 mutant；
- `examples/config-snapshot.json`，storage-snapshot profile；
- `LargestTerm=10`、`MaxLogIndex=10`、`MaxValue=5`；
- snapshot threshold=3、retain entries=1；
- strict controlled TLC：`raft-storage-snapshot-10.cfg`；
- candidate budget=15、total Action budget=1500、max PlanAction=140；
- per-Waypoint budget=15、Frontier top-K=6；
- workers=1、save-all-runs=true、replay verification=true；
- seeds：101、202、303；
- 每个 Goal × 方法实际运行 3 个 seed，共 18 个搜索。

原要求建议每组 10 个 seed。本轮只做机制验证，18 个搜索已经产生 195 个完整候选
artifact 和对应 strict TLC 执行；为避免把本轮扩成长跑，降为 3 个 seed。这个数量
不足以做统计显著性判断。成功后会提前停止，因此预算是相同上限，实际候选和 Action
不是强行消耗到相同数。

## 17. 实际 Goal 与 Waypoint 结果

| Goal | 方法 | 成功 seed | Reach rate | 平均候选 | 平均 Action | 平均墙钟 | 最常停留 |
|---|---|---:|---:|---:|---:|---:|---|
| A | Unguided | 0/3 | 0% | 15 | 190.7 | 2.112 s | W2 |
| A | Goal-aware only | 0/3 | 0% | 15 | 689 | 6.506 s | W4 |
| A | Frontier | 3/3 | 100% | 10 | 136 | 2.064 s | W6 |
| B | Unguided | 0/3 | 0% | 15 | 190.7 | 1.990 s | W2 |
| B | Goal-aware only | 3/3 | 100% | 5 | 80 | 0.778 s | W2 |
| B | Frontier | 3/3 | 100% | 5 | 40 | 0.624 s | W2 |

Waypoint reach rate：

- Goal A / Unguided：W1 100%，W2–W7 0%；
- Goal A / Goal-aware：W1–W3 100%，W4–W7 0%；
- Goal A / Frontier：W1–W7 全部 100%，W5→W6 平均需要 5 个候选；
- Goal B / Unguided：W1 100%，W2 33.3%，W3–W6 0%；
- Goal B / Goal-aware 与 Frontier：W1–W6 全部 100%。

Goal B 的 W4 与 W5 可在同一个 Restart frame 连续满足，所以 W4→W5 的候选差为 0；
Goal A 的 W6 投递同时产生 Snapshot 安装 Effect，所以 W6→W7 也可为 0。这不是提前
读取最终状态，而是同一 Concrete Action 的有序因果结果。

## 18. Frontier 是否有独立收益

Goal A 有初步证据：相同 hints 下 operators-only 3/3 失败并停在 W4，Frontier 3/3
成功到 W7；而 Frontier 平均 Action 反而更少（136 对 689）。原因是 operators-only
反复执行并继续扩张完整 Plan，Frontier 从 W4/W5/W6 的短前缀继续，并利用 Distance
保存 MsgApp → rejection → MsgSnap 的中间链。

Goal B 不能把“成功”归因于 Frontier：operators-only 与 Frontier 都是 3/3、平均
候选都是 5，说明 operator hints 已解释本次 reach 收益。Frontier 的独立信号只是
平均 Action 从 80 降到 40；样本不足，暂时只能称为重复前缀开销下降。

## 19. Goal progress 与 Facet novelty

代表性每-seed coverage：

| Goal/方法 | v1 | v2 | E/R/S/Rec/N Facet | 四种 Interaction |
|---|---:|---:|---|---|
| A / Goal-aware | 80 | 40 | 6/22/6/1/2 | 5/7/4/3 |
| A / Frontier | 42 | 39 | 6/22/8/2/4 | 6/11/7/3 |
| B / Goal-aware | 18 | 18 | 11/9/1/4/1 | 5/5/3/7 |
| B / Frontier | 18 | 18 | 11/9/1/4/1 | 5/5/3/7 |

Interaction 顺序为 election-network、replication-network、snapshot-recovery、
recovery-term-relation。

关键观察：

- Goal A operators-only 覆盖更多 v1 状态（80 对 42），却没有达到目标；
- Unguided 每个 seed 有 6–7 次“新 Facet 但全局 Goal 没推进”，仍未达到目标；
- Frontier 的目标推进在本样本中都伴随新 Facet；
- 因此 Facet 广度与特定行为目标互补，不能互相替代。

这也说明“覆盖数字更大”不自动代表更接近一个故障恢复场景。

## 20. Runtime、TLC 与 Oracle

- 共 195 个候选 run；
- 195/195 Runtime status 为 `completed`；
- 195/195 执行 strict TLC；
- model failure、Runtime failure、Oracle finding 均为 0；
- 39/39 Frontier prefix replay 成功；
- 0 次候选 Plan prefix 执行不一致；
- online/offline mismatch 为 0；
- LLM 调用为 0。

Goal 达成只说明场景真实发生，不代表发现 bug。本轮使用正常实现，不注入 mutant，
也没有试图用“无 finding”证明系统正确。

## 21. 测试覆盖

新增测试覆盖：

- Schema、唯一 ID、稳定顺序/序列化、未知 Goal/节点数；
- follower 稳定绑定与节点顺序；
- Progress 词典序；
- Frontier top-K、语义去重、Corpus 隔离；
- 大 lag 不误判 snapshot-required；
- stale/same/client message 不误判 higher-term；
- 精确 MessageID、恢复未完成和 term 前进；
- 同 Trace 的 online/offline 一致；
- prefix replay；
- CLI artifact、模式校验与跨 run 聚合。

原有 v1、v2、Facet、coverage-compare/factorize、Replay、Mapper、Oracle、
Runtime、snapshot directed policy 和默认 Corpus 继续由全量测试覆盖。

最终验证结果：

```text
GOCACHE=/tmp/modelfuzz-ng-waypoint-gocache \
GOPATH=/tmp/modelfuzz-ng-waypoint-gopath go test ./...
PASS（全部 package）

GOCACHE=/tmp/modelfuzz-ng-waypoint-gocache \
GOPATH=/tmp/modelfuzz-ng-waypoint-gopath go vet ./...
PASS

git diff --check
PASS
```

## 22. 兼容性

默认 `Engine.Run` 仍走原路径；只有 `RunObserved` 创建 PrefixObserver。没有修改：

- `raft-coverage-v1`；
- `raft-coverage-v2-prototype`；
- `raft-coverage-facets-v1-prototype`；
- 默认 Corpus admission；
- 默认 mutation energy/FIFO；
- strict TLC、Mapper、Oracle、Runtime 行为；
- snapshot-partition directed policy。

Frontier 使用独立 package 和 artifact，单元测试确认操作 Frontier 不改变一个新建
Corpus 的 snapshot。

## 23. 已知限制

1. 只有 2 个手工 Goal、3 个 seed，且 operator 大多确定性，seed 多样性不足；
2. `workers` 当前必须为 1，没有并行 Frontier；
3. 没有 checkpoint/resume；
4. top-K=6 的小实验没有 eviction，真实多样性压力尚未验证；
5. Distance 是人工离散层级，需要更多反例审查；
6. Goal 与 operator 共享人工知识，存在 evaluator/operator 耦合；
7. 在线 evaluator 不消费逐步 TLC State，因为 TLC 当前是整条 Trace 后批量执行；
8. 成功即停止，实际消耗低于预算上限，覆盖数量不适合直接按总运行量比较；
9. 没有引入 mutant、外部 issue 或新 bug 评测；
10. 没有实现 directed policy A 的同预算参考上界；
11. 汇总统计是小样本描述，不是显著性检验；
12. 当前 Goal 只支持静态 3/5 voter，不支持 membership、PreVote、CheckQuorum、
    真实磁盘或外部进程 backend。

## 24. 下一轮建议与证据

选择方向 A，暂不进入 Goal-local stall 或 LLM：

1. 至少扩展到 10–20 个真正产生不同调度的 seed，并让 operator 在合法候选中保留
   可控随机性；
2. 为 Goal A 加入 snapshot-partition directed policy 同预算参考上界；
3. 增加 top-K=1/2/4/8 消融，确认收益来自 Frontier，而不是某个 Distance 特例；
4. 对 Goal B 弱化 hints 或加入多个可选 higher-term 路径，检验 Frontier 的独立性；
5. 增加人工 mutant 与已知 issue 场景，检查 Goal search 是否提高 bug 检出率；
6. 审查 W4/W6 Distance 和 invalid 分支，补更多因果反例；
7. 只有当多 seed 下 Frontier 稳定优于两个基线、replay 仍稳定，才考虑
   Goal-local stall；在 stall 输入稳定之前不设计真实 LLM 调用。

本轮完成并评估后停止，没有自动进入下一轮。
