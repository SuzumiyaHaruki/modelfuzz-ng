# Feedback tuning 与 checkpoint v7 实验（2026-07-22）

## 目标

本轮针对30分钟长跑中 crash/restart 占 Action 48.19%、Corpus 准入率86.3%的现象，
逐项验证以下改进：降低在线 crash 权重、限制故障周期、降低故障对变异概率、提高
原始状态门槛，以及使用 Raft 语义状态/转移覆盖决定 Corpus 准入。

全部实验只使用本地 etcd-raft 内存节点和 `http://127.0.0.1:2025` 的 controlled TLC，
模型边界为 LargestTerm=10、MaxLogIndex=10。

## 实现

- balanced 在线策略默认 `Crash=1`、`Restart=10`；每条轨迹最多4个 crash 周期，
  相邻周期至少间隔48个 Action。Restart 不受 cooldown 限制。
- 本地结构变异插入 crash/restart pair 的默认概率由约1/6改为5%，并复用相同的
  周期总量与间隔校验。
- CLI 默认 `-min-new-model-states=25`；原始 TLC fingerprint 无论是否保留 Plan，
  都会进入全局统计和 checkpoint 覆盖集合。
- 语义投影保留 active set、节点角色、相对 term、日志 term/value 形状、提交进度、
  replication lag、投票关系；忽略绝对 term 和 nextIndex 的内部记账差异。
- 语义转移键为 `(projected_before, model_action_class, projected_after)`。
- checkpoint v7 保存原始状态、语义状态、语义转移三类紧凑键，使恢复后的准入决策
  与不中断运行一致。

## 逐项实验

100-run 对照统一使用 seed=880100、每条最多300个 PlanAction。前四组为了隔离变量，
显式关闭语义准入；最终组使用全部默认改进。

| 组 | 主要变化 | Corpus | 准入率 | Action | crash+restart | Ready峰值 |
|---|---|---:|---:|---:|---:|---:|
| A | 近似旧策略：Crash=5、无 cooldown、pair=17%、raw=1 | 74 | 74% | 21,181 | 3.13% | 31 |
| B | 仅 Crash 权重降到1 | 68 | 68% | 19,723 | 2.58% | 26 |
| C | 再加 cooldown=48、最多4周期 | 68 | 68% | 19,713 | 2.57% | 26 |
| D | 再将 pair 降到5% | 72 | 72% | 19,820 | 2.46% | 26 |
| E | 再将原始门槛提高到10 | 62 | 62% | 25,619 | 2.11% | 23 |
| G | 最终默认：raw=25且开启语义准入 | 44 | 44% | 22,859 | 2.30% | 7 |

A-D 的短反馈实验没有复现旧长跑的故障病理，因此另用旧长跑的四个初始 seed
470721..470724、1000 Action 做在线策略专项对照：

| 策略 | Action | crash | restart | crash+restart占比 | deliver |
|---|---:|---:|---:|---:|---:|
| 旧权重、无边界 | 4,000 | 962 | 959 | 48.03% | 495 |
| 只把 Crash=1 | 4,000 | 704 | 702 | 35.15% | 713 |
| 再加48 cooldown与4周期上限 | 2,445 | 12 | 12 | 0.98% | 1,676 |

这说明只降权重不足以消除状态相关的 crash/restart 循环，硬上限和 cooldown 才是
主要修正。最终100-run 中 crash/restart 共526/22,859=2.30%，仍能测试恢复，但不再
占用近半预算。

## 语义覆盖与最终100-run

最终目录：`runs/feedback-tuning-g-final-100-20260722`。

- succeeded=100，failed=0，runtime failure=0；
- raw states=3,141；semantic states=2,123；semantic transitions=3,396；
- Corpus=44，peak ready=7；
- message_not_available=1,233，selector_start_clamped=429；
- model_bound_reached=23，timeout_term_bound=7；
- checkpoint=384 KiB，corpus.jsonl=952 KiB。

语义投影在该样本中将原始状态数压缩约32.4%。相同 raw=25 的短对照中，开启和关闭
语义准入都保留44条：早期每条达到25个新原始状态的轨迹也都产生了新语义转移。
因此本实验确认语义覆盖已经正确计算、统计和持久化，但它的额外筛选效果需要在较长
实验、语义空间趋于饱和后观察；当前主要收紧来自 raw=25。

## checkpoint 中断恢复

`runs/feedback-tuning-resume-40-20260722` 在完成5条后由 SIGINT 中断并从 checkpoint
恢复；`runs/feedback-tuning-resume-control-40-20260722` 使用相同 seed=881000 不间断执行。
两者最终均为 completed=40、succeeded=39、failed=1、Corpus=16、Action=10,018、
raw states=1,179。去除耗时、吞吐和时间线后 report 完全一致，`corpus.jsonl` 逐字节
一致，checkpoint 的配置、聚合集合、Corpus 覆盖和调度水位也一致。

两组在相同 index=38 都得到 TLC `disabled_action(ClientRequest)`，证明这是确定性的
随机变异/抽象模型可执行性差异，不是恢复引入的漂移，也不是 runtime_error。后续保存
完整产物到 `runs/feedback-tuning-disabled-action-artifacts-40-20260722` 并回放后定位到：

- concrete step 111/112 向节点2投递了 `prevIndex=3` 的批量 `MsgApp`；
- 此时节点2 `committed=5`，etcd-raft 的 `handleAppendEntries` 直接回复
  `MsgAppResp.Index=5`，没有检查或追加任何 entry；
- Mapper 误把批次中的全部 entry 逐条送进 TLC，使模型日志由6提前增长到9；
- 节点2再次成为 leader 后，TLC 日志先到 MaxLogIndex=10，故 event 125 的正常
  `ClientRequest` 被错误判成 disabled，而 SUT 日志当时只有7条。

Mapper 现按 etcd-raft 的 committed-prefix 快速返回语义，把 `prevIndex < committed` 的
`MsgApp` 映射为保持日志和 commit 不变的 nil append，只保留可能的 term/role 更新。
同一份 seed=881038、200 Action plan 回放到
`runs/feedback-tuning-disabled-action-fixed-20260722` 后为 `status=completed`、146个模型
事件、147个模型状态、0个 Oracle finding。

## Corpus 准入原因统计

`runs.jsonl` 现在逐条记录 `new_raw_states`、`new_semantic_states`、
`new_semantic_transitions`、稳定的 `corpus_admission` 原因码和每100 Action的语义 novelty。
报告和 `experiment-metrics.json` 同时聚合 raw 门槛拒绝、无语义新颖性拒绝，以及由
语义状态/语义转移保留的次数；checkpoint 恢复校验原因码总数、Corpus 水位和派生计数。

本地 TLC 烟雾目录 `runs/corpus-admission-stats-smoke-30-20260722`：30/30成功，
Corpus=12；`rejected_raw_threshold=18`、`rejected_no_semantic_novelty=0`，12条保留轨迹
均同时贡献新语义状态和转移；raw/语义状态/语义转移覆盖分别为860/635/972，合计语义
novelty 密度为23.94/100 Action。该密度统计所有成功执行产生的全局新语义覆盖，包括
因 raw 门槛未进入 Corpus 的运行，避免把“覆盖贡献”和“反馈保留”混为一谈。

## Mapper 修复100-seed回归

目录：`runs/mapper-fix-regression-100-20260722`。使用 seed=881000、100轮、每条最多
300个 PlanAction、LargestTerm/MaxLogIndex=10/10：

- succeeded=100、failed=0、runtime_error=0、TLC disabled action=0；
- `raft.proposal_dropped=211`，事件继续进入统计，但没有变成运行错误；
- Action=21,359，raw/语义状态/语义转移覆盖=2,755/1,988/3,125；
- Corpus=43，raw门槛拒绝57，语义门槛独立拒绝0，Ready峰值7；
- 强制timeout=697（3.26%），`message_not_available=699`（3.27%）；
- `model_bound_reached=16`。

这组结果与保存的 seed=881038 完整 Plan 回放共同确认：committed-prefix `MsgApp`
修复消除了已知确定性假失败，`proposal_dropped` 的 stutter/统计口径也保持正确。

## 1000-run语义准入A/B

两组均使用 seed=883000、raw门槛25、1000轮、每条最多300个 PlanAction，只改变
`semantic-coverage`。目录分别为：

- A：`runs/corpus-ab-semantic-on-1000-20260722`；
- B：`runs/corpus-ab-semantic-off-1000-20260722`。

| 指标 | A：semantic on | B：semantic off |
|---|---:|---:|
| succeeded / failed | 1000 / 0 | 1000 / 0 |
| Action | 232,379 | 231,680 |
| Corpus | 393 | 393 |
| raw门槛拒绝 | 605 | 607 |
| 无语义novelty拒绝 | 2 | 0 |
| raw状态覆盖 | 29,026 | 28,915 |
| 语义状态覆盖 | 19,928 | 19,858 |
| 语义转移覆盖 | 31,713 | 31,637 |
| 语义novelty / 100 Action | 22.2227 | 22.2268 |
| 唯一模型状态路径 | 802 | 798 |
| mutation / random init执行 | 782 / 218 | 780 / 220 |
| Ready峰值 / discarded mutation | 7 / 0 | 7 / 0 |
| proposal dropped | 1,819 | 1,798 |
| runtime error | 0 | 0 |
| model bound reached | 222 | 224 |
| checkpoint / corpus.jsonl / runs.jsonl | 3.4 / 8.3 / 4.1 MiB | 3.4 / 8.3 / 4.1 MiB |

两组在run 0..401的 Plan和Trace完全相同。首次准入分歧发生在run 402：该 mutation
新增48个 raw TLC fingerprint，但没有新增语义状态或语义转移；A组以
`rejected_no_semantic_novelty` 拒绝，B组保留。两组从run 403开始进入不同反馈分支。
A组第二次语义拒绝发生在run 722，对应81个新增raw状态、0个新增语义状态/转移。

到1000轮时，A组比B组多111个raw状态、70个语义状态、76个语义转移和4条唯一状态
路径，但也多执行699个Action；归一化语义novelty反而仅低0.0041/100 Action，差异可
视为噪声级。结论是语义门槛已经正确工作并能拒绝“raw新、语义旧”的轨迹，但当前只
影响0.2%的执行，raw=25仍是主要准入门槛。现阶段没有证据支持进一步收紧语义门槛；
应先在更长实验中观察其拒绝率是否随语义空间饱和而上升。

## 修复后30分钟稳定性实验

代码先冻结为本地 commit `d3c5e91c3479ef37f3504e2db760cc3d8778b9e0`，从该提交
构建二进制。目录：`runs/feedback-soak-30m-postfix-20260722`。实验使用 seed=884000、
raw门槛25、semantic on、每条最多300个 PlanAction、LargestTerm/MaxLogIndex=10/10、
checkpoint每100轮、只保存失败产物。总运行上限设为100,000轮，由外层30分钟 timeout
发送 SIGINT 正常停止；最终退出码124和`context canceled`属于预期时间边界。

最终结果：

| 指标 | 结果 |
|---|---:|
| elapsed | 1,801.024秒 |
| completed / succeeded / failed | 8,197 / 8,197 / 0 |
| Action / model event | 1,905,565 / 1,215,917 |
| actions/s / runs/s | 1,058.05 / 4.55 |
| raw状态覆盖 | 232,471 |
| 语义状态 / 转移覆盖 | 160,867 / 256,805 |
| 语义novelty / 100 Action | 21.9185 |
| Corpus / 准入率 | 3,293 / 40.17% |
| raw门槛拒绝 | 4,879（59.52%） |
| 无语义novelty拒绝 | 25（全部执行0.305%，raw合格轨迹0.753%） |
| mutation / random init执行 | 6,583 / 1,614 |
| Ready峰值 / discarded mutation | 17 / 0 |
| runtime error / TLC failure | 0 / 0 |
| proposal dropped | 17,027 |
| model bound reached | 1,588（19.37%） |
| checkpoint / corpus.jsonl / runs.jsonl | 28 / 68 / 34 MiB |

Action分布中 Deliver=59.53%、强制timeout=2.36%、crash+restart=2.28%。
`message_not_available=101,207`（5.31% Action），`selector_start_clamped=40,021`
（2.10%），`timeout_term_bound=44`。proposal dropped占Action约0.89%，全部作为可见
stutter统计，没有进入runtime error。

按每1000轮分窗，Corpus准入率保持在38.5%–42.1%，无语义novelty拒绝分别为
1、4、2、6、2、1、4、5次；每窗语义novelty保持在21.37–22.76/100 Action，没有出现
明显的语义空间饱和。semantic gate在长跑中持续正确工作，但独立筛选仍不足1%，
raw=25继续主导准入；当前数据不支持进一步收紧语义门槛。

JVM启动RSS约81 MiB，首个100-run checkpoint后约211 MiB，随后缓慢增长并经历GC回落，
实验结束前约253 MiB；30分钟内没有OOM或单调快速膨胀。本次没有复现此前 eager
Action 大量常驻导致的短时间OOM，支持按需创建Action的修改有效。checkpoint约
3.5 KiB/完成轮，远小于旧2小时实验约11 KiB/轮且不再包含膨胀Ready队列；本次Ready
峰值只有17。

## 验证

- `gofmt`：通过；
- `go test ./...`：通过；
- `tools/tlc-server/test.sh`：通过，包括 lazy/eager Action 等价测试；
- 无 TLC 10-run：10/10成功，Corpus按预期为0；
- TLC 最终100-run：100/100成功；
- checkpoint v7 中断恢复：与不中断 control 的确定性字段一致。
- seed=881038 的历史 TLC 假失败轨迹修复后完整回放通过；
- Corpus 准入统计30-run TLC烟雾通过，checkpoint 聚合校验与逐run字段一致。
- Mapper修复100-seed回归100/100通过，proposal dropped可见且不计为runtime error；
- semantic on/off同seed 1000-run A/B均1000/1000通过，首次分歧和最终覆盖已记录。
- 修复后30分钟soak完成8,197轮和约190.6万Action，零失败、无JVM OOM；timeout停止符合预期。
