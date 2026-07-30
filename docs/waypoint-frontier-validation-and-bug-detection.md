# Waypoint Frontier 稳定性、消融与缺陷检出（第四轮）

日期：2026-07-28
Goal schema：`raft-behavior-goals-v1-prototype`
Benchmark schema：`raft-goal-benchmark-v1`

## 1. 本轮回答什么问题

第三轮只有 3 个 seed，且 strong hint 很接近人工编写的下一步动作，因而只能证明机制
可行。本轮把方向 A 做成可重复的 10-seed 实验，回答：

1. Frontier 的收益能否跨 seed 重复；
2. 收益来自 hint、前缀复用还是 Distance；
3. top-K 是否真的带来有用多样性；
4. 通用 Goal Evaluator 与手工 Snapshot policy 还有多大差距；
5. Goal 搜索能否提高匹配 mutant 的检出率或缩短首次检出；
6. 当前证据是否足够进入 Stall Detector 或 LLM Planner。

本轮没有接入 LLM，所有报告和汇总的 `llm_calls` 都是 0。

## 2. 已实现能力

### 2.1 Hint strength

- `none`：普通 fuzz 仍调用原有 `mutation.Random`；Goal 只观察，不影响变异。
- `weak`：只改变 Action 类别和消息类别的权重，不读取最终 MessageID，也不直接拼出
  某个 Waypoint 的完整动作。
- `strong`：可使用稳定的 Leader/TargetFollower 绑定、当前 Waypoint 和已经观测到的
  精确 MessageID，生成局部下一步。

`none`、`weak` 和 `strong` 都使用确定性 seed。单元测试确认 `none` 不写入 Goal
元数据，`weak` 不选择精确 MessageID，默认 fuzz 路径没有被替换。

### 2.2 Frontier 和无前缀消融

正式 Frontier 保存达到当前最好进展的 Plan/Trace 前缀，先 replay 验证，再只变异后缀。
Frontier seed 现在持久化完整 `GoalInstance`，避免“选中了 seed A，却沿用上一候选的
MessageID/evidence”。

`frontier-no-prefix-preservation` 仍从 Frontier 选择父 Plan，但允许删除或改动已完成
阶段中的 Action，不把该运行标记为 prefix-preserved，也不声称完成了前缀 replay。
它专门用于判断收益是否真的来自因果前缀保护。

### 2.3 Distance 和 Snapshot MessageID

两种 Distance 使用完全相同的 Waypoint predicate 和最终成功条件：

- `boolean-only`：未满足为 1，满足为 0；
- `staged-distance`：区分尚无边界、接近压缩边界、已要求 Snapshot、MsgSnap
  可投递/被分区阻塞，以及精确消息已投递等阶段。

Goal A 的 W6 在首次看到 pending `MsgSnap` 时绑定具体 MessageID；后续只有投递这个
ID 才算 W6，不能由同链路另一条 Snapshot 消息代替。

### 2.4 批量实验、统计与多样性

新增 `goal-benchmark`：

- manifest 中每个 Campaign 只包含一个 Goal；
- 每个 Goal、方法和 seed 使用独立输出目录与独立 Frontier；
- 完成的 `final-report.json` 可以跳过，失败 Campaign 不覆盖其他结果；
- 从逐 seed 报告重建 comparison JSON 和 figure-ready CSV；
- 保存完整命令、有效配置、环境、seed manifest 和纳入/排除原因。

汇总按 Goal、方法、hint、top-K、prefix、Distance 和 control/mutant 分组，避免把消融
组合错误合并。比例报告 95% Wilson 区间；成功 time-to-target 与失败
time-to-first-failure 分开统计，包含 mean、median、样本标准差、IQR、min/max 和原始值。
没有进行显著性检验，也不宣称统计显著。

seed 多样性同时保存：

- 初始 Plan 精确键；
- 最终 Trace 精确键；
- 去掉绝对 term/index 的相对语义 Trace 键；
- Goal progress path；
- Facet sequence；
- Frontier prefix 语义键；
- message queue shape。

因此“seed 数不同”和“实际调度语义不同”不会混为一谈。

## 3. 公平预算与环境

稳定性和消融的共同配置如下。

| 项目 | 值 |
|---|---|
| seed | 4101–4110 |
| 节点 | 3 |
| candidate budget | 20 |
| total action budget | 3000 |
| max actions / Plan | 160 |
| per-Waypoint budget | 20 |
| Snapshot threshold / retain | 3 / 1 |
| crash quota | 2 |
| partition | enabled |
| worker | 1 |
| model | `raft_storage_snapshot.tla` |
| TLC | strict，TLC 1.8.0，本机 `127.0.0.1:22061` |
| etcd-raft | v3.7.0，本地 `replace ../raft` |

三种主要方法使用相同 Goal、seed、配置和预算。Snapshot directed policy 是人工参考
上界，固定每 seed 只执行一个候选，因此不并入“三种方法谁更好”的公平比较。

运行环境由 `environment.json` 保存：Go 1.26.4、Linux/amd64、AMD Ryzen 7 6800H，
Git revision `75d4e51120b370acb880d003629f916da3f1a080`，并明确记录
`vcs.modified=true`。这表示实验包含本轮未提交改动，不能只靠该 commit 恢复，必须
同时保留本轮源代码和 manifest。

## 4. 实际实验矩阵

稳定性与关键消融共 14 个 Campaign、140 个运行，全部完成，没有 Campaign 失败：

- Goal A：unguided、strong goal-aware-only、strong Frontier；
- Goal B：unguided、strong goal-aware-only、strong Frontier；
- Goal B hint：weak goal-aware-only、weak Frontier；
- Goal A top-K：1、2、4、8；
- Goal A prefix：正式 Frontier 与 no-prefix；
- Goal A Distance：staged 与 boolean-only；
- Goal A directed policy 参考。

没有运行完整参数笛卡尔积。例如没有对 Goal B 重复 top-K/Distance 消融，也没有把
directed policy 用到 Goal B。这样做是为了让计算集中在能回答归因问题的组合。

## 5. 三种主要方法

### 5.1 Goal A：Partition 后通过 Snapshot 追赶

| 方法 | Goal reach | 95% Wilson | 首次成功候选 mean/median | 首次成功 Action mean/median | 时间 mean |
|---|---:|---:|---:|---:|---:|
| unguided | 0/10 | 0–27.8% | — | — | — |
| strong goal-aware-only | 0/10 | 0–27.8% | — | — | — |
| strong Frontier，K=4 | 10/10 | 72.2–100% | 10 / 10 | 136 / 136 | 1561 ms |

Goal-aware-only 的所有 seed 到 W3，但停在 W4；Frontier 的所有 seed 到 W7。相同
strong operator 仍出现 0/10 对 10/10，说明 Goal A 的成功不能只由“增加相关 Action
概率”解释。

### 5.2 Goal B：Restart 后处理 higher-term 消息

| 方法 | Goal reach | 95% Wilson | 首次成功候选 mean/median | 首次成功 Action mean/median | 时间 mean |
|---|---:|---:|---:|---:|---:|
| unguided | 0/10 | 0–27.8% | — | — | — |
| strong goal-aware-only | 10/10 | 72.2–100% | 5 / 5 | 80 / 80 | 583 ms |
| strong Frontier | 10/10 | 72.2–100% | 5 / 5 | 40 / 40 | 484 ms |

Goal B 的达成主要由 strong hint 解释；Frontier 没有提高 10/10 的到达率，但把首次
成功 Action 从 80 降为 40。不能据此声称 Frontier 对所有 Goal 都有独立到达率收益。

## 6. Hint strength 消融

Goal B 的 weak hint 结果：

| 方法 | Goal reach | 首次成功 Action（成功 seed） | W3/W4/W5/W6 到达 |
|---|---:|---:|---:|
| weak goal-aware-only | 0/10 | — | 4/10、4/10、4/10、0/10 |
| weak Frontier | 3/10 | mean 70.7，median 67 | 7/10、6/10、6/10、3/10 |

weak 模式下 Frontier 仍有 3/10 对 0/10 的独立信号，说明其作用不完全依赖精确
MessageID strong hint。但样本只有 10，Wilson 区间较宽（Frontier 10.8–60.3%），还
不足以称为稳定泛化收益。

## 7. Top-K 消融

Goal A strong Frontier：

| K | Reach | 候选 mean | Action mean | 时间 mean | replay | eviction |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | 10/10 | 8 | 105 | 1276 ms | 70/70 | 20 |
| 2 | 10/10 | 9 | 121 | 1439 ms | 80/80 | 10 |
| 4 | 10/10 | 10 | 136 | 1561 ms | 90/90 | 0 |
| 8 | 10/10 | 10 | 136 | 1508 ms | 90/90 | 0 |

本轮 K=1 最省，K 增大没有提高到达率。四组 strong Frontier 的相对语义 Trace 每组
都只有 1 种，说明当前 deterministic strong operator 没有利用更大 K 形成有意义的
语义分支。阶段 3 因而选择 K=1；这不是“普遍最优 K”，只是在当前 Goal/operator 下
成本最低的代表配置。

## 8. Prefix preservation 消融

| 配置 | Reach | 最远 Waypoint | 选择 Frontier seed | Waypoint regression | 被破坏的已完成阶段 |
|---|---:|---:|---:|---:|---:|
| 正式 Frontier，K=4 | 10/10 | W7 | 90 | 0 | 0 |
| no-prefix | 0/10 | W2 | 190 | 180 | 218 |

no-prefix 仍选择 Frontier 父 Plan，但允许改动完整 Plan。它大量破坏已经完成的阶段，
并且 0/10 达成目标。这一消融直接支持：Goal A 的收益来自保存因果前缀，而不只是
“曾经选过一个更好的父 Plan”。

## 9. Distance 消融

| Distance | Reach | W1–W5 | W6/W7 | replay |
|---|---:|---:|---:|---:|
| staged | 10/10 | 全部 10/10 | 10/10、10/10 | 90/90 |
| boolean-only | 0/10 | 全部 10/10 | 0/10、0/10 | 190/190 |

两者目标谓词完全相同；差异只来自 Frontier 在 W6 内能否区分“尚未生成 Snapshot、
已生成但被阻塞、精确消息可投递”等进度。结果支持 staged Distance 对当前 Goal A
是必要的。它仍是人工离散阶段，不应被解释为跨 Goal 的连续数学距离。

## 10. Snapshot directed policy 参考

手工 directed policy 在 10/10 seed 中均由通用 Goal Evaluator 判为成功，
online/offline mismatch 为 0；每次 1 个候选、25 个 Action、平均约 245 ms。
Frontier K=1 需要 8 个候选、105 个累计 Action。两者相差 80 个 Action，表明通用搜索
已能稳定达到目标，但还没有接近人工完整策略的成本上界。`policy_complete` 没有被
用作 Goal 成功条件。

## 11. Seed 选择与实际多样性

140 个稳定性运行的全局统计为：

- 4 种实际初始 Plan；
- 97 种精确最终 Trace；
- 37 种相对语义 Trace；
- 48 种 Goal progress path。

必须按 Campaign 解读这些数字。主要方法的初始 Plan 都只有 1 种；strong Frontier
虽然因绝对 term/index、MessageID 等差异得到 10 种精确 Trace，但每个 Campaign
都只有 1 种相对语义 Trace和 1 种 progress path。unguided 的 Goal A 有 10 种相对
语义 Trace和 8 种 progress path，仍为 0/10。

因此 10 个 seed 排除了“只运行三次”的问题，却没有排除 strong operator 导致调度
塌缩的问题。这是本轮选择继续方向 A 的主要负证据之一。

## 12. Frontier、replay 与在线/离线一致性

稳定性矩阵中：

- 正式 Frontier 和 boolean 消融共完成 721/721 次 prefix replay；
- prefix execution mismatch 为 0；
- 140 个运行的 online/offline Goal mismatch 为 0；
- no-prefix 模式没有把父 Plan 选择错误记成 replay-preserved。

这些数据支持 Frontier 前缀可以确定性复用。它们不证明任意外部进程、真实网络或
真实物理时间 backend 也有相同确定性。

## 13. Goal progress 与 Facet novelty

140 个运行合计观察到：

- `new_facet_without_goal_progress = 353`；
- `goal_progress_without_new_facet = 6`；
- `new_waypoint_without_new_facet = 6`；
- `distance_improvement_without_new_facet = 0`。

当前证据能说明 Facet 新颖性经常与指定 Goal 无关，不能用“出现新 Facet”替代 Goal
进展；同时有 6 次新 Waypoint 没有新 Facet，说明 Goal 也能补充 Facet 没区分出的
因果顺序。两者是互补信号，而不是可以互相替代的单一分数。

## 14. Mutant 与 control

本轮只使用仓库中已有且已有实验支持的相关 mutant：

- Goal A：Snapshot status 映射成败取反；
- Goal B：Restart 丢失 HardState。

Snapshot-status mutant 模拟 Adapter/Mapper 观察链错误，不是 etcd-raft 上游缺陷；
restart mutant 模拟错误的持久化恢复契约，而且注入较强。每个 mutant 都有未修改
control、相同 seed、Goal、方法和预算。quorum mutant 与两个 Goal 关系较弱，本轮
没有强行纳入。

### 14.1 Goal A 与 Snapshot-status mutant

| 方法 | Control Goal/Bug | Mutant Goal/Bug | 首次失败 candidate | 首次失败 Action | 首次失败时间 |
|---|---:|---:|---:|---:|---:|
| unguided | 0/10，0/10 | 0/10，0/10 | — | — | — |
| strong goal-aware-only | 0/10，0/10 | 0/10，0/10 | — | — | — |
| strong Frontier K=1 | 10/10，0/10 | 0/10，10/10 | 8 / 8 | 105 / 105 | mean 1191 ms，median 1180 ms |

所有 control 的 false-positive rate 都是 0。Frontier 的 10 个 mutant 都在 W6
之后、试图用 Snapshot status 完成 W7 的 Action 上得到 `mapping_failed`；归一化
签名相同。因为 Mapper 在把该 Action 交给 Goal evaluator 前就拒绝错误映射，报告
严格记为 `before-goal`，而不是把“即将完成 W7”倒推成已经达成 Goal。

这组结果把“到达深层行为”和“发现错误”连接起来：两种到不了 Snapshot status
边界的方法也没有检出该 mutant，Frontier 同预算下为 10/10。它证明的是匹配人工
mutant 的测试价值，不是发现了新的 etcd-raft 生产缺陷。

### 14.2 Goal B 与 Restart 丢失 HardState mutant

| 方法 | Control Goal/Bug | Mutant Goal/Bug | Bug detection | 首次失败 candidate mean/median | 首次失败 Action mean/median | 时间 mean/median |
|---|---:|---:|---:|---:|---:|---:|
| unguided | 0/10，0/10 | 0/10，7/10 | 70%，Wilson 39.7–89.2% | 10.3 / 9 | 173 / 88 | 1134 / 574 ms |
| strong goal-aware-only | 10/10，0/10 | 0/10，10/10 | 100%，Wilson 72.2–100% | 4 / 4 | 60 / 60 | 428 / 431 ms |
| strong Frontier K=1 | 10/10，0/10 | 0/10，10/10 | 100%，Wilson 72.2–100% | 4 / 4 | 32 / 32 | 375 / 379 ms |

20 个 guided mutant 运行都在 W6 前由基础 Oracle 报
`raft.basic:term_regressed`；unguided 的 7 个失败分散在 W1–W3。Frontier 和
goal-aware-only 的检出率相同，说明 strong hint 已足够稳定激活这个较强 mutant；
Frontier 的价值体现在首次失败累计 Action 从 60 降到 32，而不是提高 100% 的检出率。

### 14.3 Goal reach 与 Bug detection

两类 mutant 的失败都发生在 Goal predicate 真正成立之前，因此 mutant 组的 Goal
reach 是 0，并不表示搜索无效；恰恰是目标路径上的错误阻止了 Goal 完成。反过来，
能够到达目标的三个 guided control 组都为 10/10 且 0 failure；Goal A
operators-only 的 control 与稳定性实验一致，仍停在 W4。汇总分别保存 Goal reach、
Bug detection、failure relation 和 failure Waypoint，没有把两个比例相加或互相替代。

## 15. 失败 replay 与 ddmin

分别选择两个 Frontier mutant 的 seed 4101：

| 失败 | 原 Trace replay | 原 Plan → 最小 Plan | 尝试/缓存命中 | 最终验证 | one-minimal |
|---|---:|---:|---:|---:|---:|
| Snapshot status mapping | 2 次均 17/17 step | 15 → 13 | 46 / 14 | 3/3 同签名 | 是 |
| Restart HardState | 2 次均 7/7 step | 7 → 4 | 22 / 5 | 3/3 同签名 | 是 |

Snapshot 最小签名为 `mapping_failed / snapshot status progress mismatch` 的规范化
数字无关形式；Restart 最小签名为
`oracle_failed / raft.basic:term_regressed`。ddmin 在开始前各重复验证原始签名
2 次，最终候选各独立验证 3 次，均未达到 200 次尝试上限。

## 16. 负结果与不能推出的结论

本轮必须保留的负结果：

- Goal A 的 unguided 和 goal-aware-only 都是 0/10；
- Goal B weak goal-aware-only 是 0/10，weak Frontier 也只有 3/10；
- 更大的 K 没有增加 strong Frontier 的语义调度多样性；
- no-prefix 和 boolean-only 都是 0/10；
- 353 次 Facet 新颖性没有带来新的全局 Goal progress；
- strong 方法跨 seed 的相对语义轨迹高度重复。

不能推出：

- Frontier 对所有 Raft 场景普遍更好；
- K=1 是其他 Goal 的最优值；
- 10/10 等于完整覆盖；
- 到达 Goal 等于发现 bug；
- 人工 mutant 是新的生产 bug；
- 当前数据具有统计显著性；
- 当前方法已经可以接入 LLM 并归因 LLM 收益。

## 17. Artifact 与复现

仓库内冻结输入：

- `examples/goal-benchmark-direction-a-stability.json`
- `examples/goal-benchmark-direction-a-mutants.json`
- `examples/config-snapshot-status-control.json`
- `examples/config-snapshot-status-mutant.json`
- `examples/config-restart-hardstate-goal-control.json`
- `examples/config-restart-hardstate-goal-mutant.json`

本机原始输出：

- `/tmp/modelfuzz-ng-round4-direction-a-stability-20260728-final`
- `/tmp/modelfuzz-ng-round4-direction-a-mutants-20260728-final`

每个根目录包含 `benchmark-manifest.json`、`benchmark-status.json`、
`environment.json`、`seed-manifest.json`、`seed-diversity.json`、
`comparison-summary.json`、`figure-ready.csv`，并在各 Campaign/seed 下保留
`goal-progress.jsonl`、`final-report.json`、Goal 定义、设置、Frontier 和必要的运行
artifact。实际完整 CLI 位于 `benchmark-status.json` 的每个 run 记录中。
代表性 replay 与 ddmin 位于 mutant 根目录的 `failure-analysis/`。

复现命令：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft_storage_snapshot.tla \
  --config models/raft/raft-storage-snapshot-10.cfg \
  --port 22061

go run ./cmd/modelfuzz-ng goal-benchmark \
  -manifest examples/goal-benchmark-direction-a-stability.json \
  -output /tmp/modelfuzz-ng-round4-direction-a-stability-rerun

go run ./cmd/modelfuzz-ng goal-benchmark \
  -manifest examples/goal-benchmark-direction-a-mutants.json \
  -output /tmp/modelfuzz-ng-round4-direction-a-mutants-rerun
```

输出目录必须是新的，或只包含可安全跳过的完整结果。

## 18. 测试与静态检查

新增测试覆盖 hint strength、K=1/2/4/8 与非法 K、no-prefix 完整 Plan 编辑、
boolean/staged 成功谓词一致、semantic trace 分组、mutant/ablation 汇总隔离、
failure signature、directed evaluator、批量 Campaign 隔离/跳过和 manifest
确定性。

最终检查：

- `go test ./...`：通过；
- `go vet ./...`：通过；
- `git diff --check`：通过；
- 两套 comparison JSON 和 CSV 从逐 seed `final-report.json` 重算后逐字节一致；
- 稳定性 140 份报告：online/offline mismatch 0、LLM calls 0；
- mutant 120 份报告：online/offline mismatch 0、LLM calls 0；
- 10 次 Snapshot `mapping_failed` 离线重算都明确标为
  `expected_offline_mapping_failures`，并与在线成功前缀一致，不再误计为 evaluator
  mismatch。

## 19. 当前判断

稳定性结果同时包含强证据和明显不足：

- Goal A 证明 prefix preservation 与 staged Distance 有实质作用；
- Goal B weak hint 给出 3/10 对 0/10 的 Frontier 独立收益信号；
- replay 和 online/offline 判断稳定；
- 相关 mutant 已证明目标路径具有测试价值，但 top-K 仍没形成语义分支，strong
  operator 的跨 seed 轨迹仍然塌缩。

本轮选择继续**方向 A**，不进入 B 或 C。理由不是机制失败，而是：

1. strong Frontier 的 10 个 seed 仍只有一种相对语义轨迹，top-K 没有真正形成分支；
2. weak Frontier 只有 3/10，独立收益还不够稳定；
3. 当前只有两个 Goal 和两个相关人工 mutant，Restart mutant 又比较强；
4. 尚未建立“正常情况下相邻 Goal progress 间隔”的分布，不能可靠设置 Stall
   Detector 阈值；
5. 在非 LLM 基线仍存在上述混杂因素时接入 LLM，无法干净归因。

下一次方向 A 应优先让候选生成产生真实的语义分支，例如在不读取精确 MessageID 的
条件下对多个合法目标、链路、故障持续时间和消息类别做结构化组合，并验证 K>1 是否
开始保留不同的 progress path。只有 weak hints 下收益更稳定、top-K 多样性可解释，
再进入方向 B 的 Goal-local Stall Detector。方向 C 仍必须在 B 之后。
