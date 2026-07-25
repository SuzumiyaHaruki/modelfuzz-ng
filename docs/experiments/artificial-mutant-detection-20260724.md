# 三类人工缺陷检测实验（2026-07-24）

## 结论

本轮实验验证了三条彼此不同的检测链：五节点 `n/3+1` 选举 quorum 缺陷主要依靠 strict TLC；snapshot-status 映射错误依靠本地 Mapper；restart 丢失 HardState 主要由 SUT panic 和基础 Raft Oracle 捕获。12 组纯随机 A/B 共执行 6,600 个 Plan、1,563,442 个 Action。所有正常对照组均为零失败，三个 mutant 均能被发现，但随机检测概率与检测层明显不同。

| mutant | strict TLC 随机结果 | 无 TLC 随机结果 | 主要检测层 |
|---|---:|---:|---|
| 五节点 `n/3+1` quorum | 100/100（100%） | 109/1000（10.9%） | TLC；无 TLC 时只能等待下游安全性破坏或 panic |
| snapshot-status 映射反转 | 77/100（77%） | 750/1000（75%） | Mapper，不依赖 TLC |
| restart 丢失 HardState | 100/100（100%） | 995/1000（99.5%） | Runtime panic 与 `term_regressed` Oracle |

这里的结果说明当前系统能够检测这些受控缺陷，不表示真实 etcd-raft 含有相同缺陷，也不能据此估计生产系统中的缺陷发生率。

## 被注入的缺陷

### `n/3+1` quorum

`faults.vote_quorum_divisor=3` 使五节点候选者在取得两票后提前成为 Leader，而正常多数派要求三票。该 mutant 改变真实 SUT 行为。对应正常配置使用 divisor 2。

### snapshot-status 映射反转

`faults.snapshot_status_mapping="invert"` 不改变传给 etcd-raft `ReportSnapshot` 的真实成功/失败结果，只把送往模型的 `raft.snapshot_status_reported.reject` 反转。它模拟 Adapter/Mapper 观察链把 snapshot 发送结果翻译错，而不是模拟 etcd-raft 自身缺陷。当 status 确实对应一个 pending snapshot 时，Mapper 根据 Leader progress 的 `pending`、`match` 和 `next` 变化识别出自相矛盾。

### restart 丢失 HardState

`faults.restart_lose_hard_state=true` 在节点 restart 前清空持久化的 term、vote、commit，同时保留日志和 applied 位置。它模拟存储恢复契约错误。这个注入故意较强：已应用位置仍为 2、恢复出的 commit 却为 0 时，etcd-raft 会拒绝这个不一致状态并 panic；若恢复暂时成功，基础 Oracle 仍可能发现 term 回退。

三个 fault 的默认值都保持正常语义，只有实验配置会显式启用。示例配置为 `examples/config-quorum-mutant.json`、`examples/config-snapshot-status-mutant.json` 和 `examples/config-restart-hardstate-mutant.json`。

## 实验设计

正式随机部分采用在线纯随机初始策略，每个 Plan 最多 300 个 Action，不进行 corpus mutation，也不以语义新颖性准入 corpus。每个 mutant 都有相同节点数、模型边界、随机种子起点和动作预算的正常 A/B 对照。strict TLC 每组 100 个 seed、并行度 1；无 TLC 每组 1,000 个 seed、并行度 4。在线策略会读取当前 Observation，因此同 seed 的 control 与 mutant 在缺陷激活后可能生成不同的后续动作；“同 seed”用于对齐初始随机流，不代表两个 Plan 始终逐项相同。

strict TLC 使用受控服务：quorum 组为 `raft.tla` 加五节点配置，snapshot-status 组为 `raft_storage_snapshot.tla`，restart 组为基础 `raft.tla`。无 TLC 组仍启用 Mapper、Runtime panic 捕获和具体 Raft Oracle。

另外执行了九个定向 control/mutant 样本，并对三个代表性失败分别完成三次完整 Plan 重现、两次 trace 回放和自动最小化。实验基于 Git 提交 `e65270462acf9fb0a4aeacfc4a7dfc7d78961c0d` 上的未提交 mutant 实现构建，因此复核应以本轮源代码和二进制摘要为准；二进制 SHA-256 为 `48f9ba23062202e0163d7a2cd219b6d01e01f95fb09657e777291d52bb5bd3c2`。

## 随机 A/B 结果

| 组 | 成功 | 失败 | 失败分类 | 执行 Action |
|---|---:|---:|---|---:|
| quorum control strict | 100 | 0 | — | 30,000 |
| quorum mutant strict | 0 | 100 | 90 `model_failed`；7 `oracle_failed`；3 `runtime_failed` | 28,689 |
| snapshot-status control strict | 100 | 0 | — | 30,000 |
| snapshot-status mutant strict | 23 | 77 | 77 `mapping_failed` | 14,913 |
| restart control strict | 100 | 0 | — | 30,000 |
| restart mutant strict | 0 | 100 | 21 `oracle_failed`；79 `runtime_failed` | 8,001 |
| quorum control 无 TLC | 1,000 | 0 | — | 300,000 |
| quorum mutant 无 TLC | 891 | 109 | 84 `oracle_failed`；25 `runtime_failed` | 286,883 |
| snapshot-status control 无 TLC | 1,000 | 0 | — | 300,000 |
| snapshot-status mutant 无 TLC | 250 | 750 | 750 `mapping_failed` | 144,830 |
| restart control 无 TLC | 1,000 | 0 | — | 300,000 |
| restart mutant 无 TLC | 5 | 995 | 182 `oracle_failed`；813 `runtime_failed` | 90,126 |

quorum mutant 的 Oracle Finding 为 `committed_log_conflict` 和 `multiple_leaders_same_term`。其中 Finding 数可以多于失败 run 数。它的 runtime failure 均为后续 `need non-empty snapshot` panic：这仍是错误 quorum 破坏 Leader Completeness 后的下游表现，不能据此宣称正常多数派路径中另有独立 missing-snapshot 缺陷。

restart mutant 的 Oracle failure 全部为 `raft.basic:term_regressed`，runtime failure 全部是恢复不一致导致的 `applied(2) is out of range [prevApplied(0), committed(0)]` panic。

## strict TLC 的独立价值

定向 quorum Plan 只有 `timeout, deliver, deliver` 三个 Action。mutant 在只有两票时成为 Leader，strict TLC 在 `BecomeLeader` 上返回 `disabled_action`；关闭 TLC 后，相同 Plan 正常结束，因为具体 Oracle 只观察到一个 Leader，无法仅凭这条短轨迹推导“票数不足”。随机 strict 组的 90 个模型失败也都是同一 `disabled_action`。

因此，strict TLC 对 quorum 缺陷不是重复检查：它把随机检测率从无 TLC 样本中的 10.9% 提高到本组的 100%，并在下游日志冲突出现前识别出第一处非法状态转换。两个样本规模不同，所以这里只能说明检测能力差异，不能把两个比例解释为精确的性能提升倍数。

snapshot-status mutant 的失败在 Mapper 中、调用 TLC 之前发生。strict 组的 TLC 服务只收到 123 个成功请求：100 个 control 加 23 个尚未触发映射矛盾的 mutant；77 个 mutant 在本地直接失败。它在 strict 与无 TLC 下分别检测 77% 和 75%，说明这一检查不依赖形式模型服务。

restart mutant 通常在 restart 动作内 panic，或者在后续映射前被 Oracle 判定 term 回退，因此 strict 组中 TLC 只处理了 100 个 control 请求。这个较强的恢复缺陷不需要 TLC 才能高概率发现。

## 激活条件与发现速度

| mutant 组 | 至少记录一次激活的 run | 激活事件数 | 首个失败 seed 偏移 | 失败前 Action 中位数 | P95 |
|---|---:|---:|---:|---:|---:|
| quorum strict | 100/100 | 255 | 0 | 300 | 300 |
| quorum 无 TLC | 1000/1000 | 2,407 | 8 | 191 | 286 |
| snapshot-status strict | 84/100 | 91 | 0 | 91 | 199 |
| snapshot-status 无 TLC | 848/1000 | 944 | 0 | 84 | 182 |
| restart strict | 21/100 | 21 | 0 | 71 | 182 |
| restart 无 TLC | 182/1000 | 182 | 0 | 78 | 204 |

quorum strict 的失败统一在完整 Plan 的模型批处理阶段报告，所以统计中的失败前 Action 都是 300；这不是说非法 Leader 一定到第 300 步才出现。无 TLC 必须继续运行到可见安全性破坏，因此发现较晚且大量 run 漏检。

snapshot-status 激活标记表示系统遇到 snapshot status 上报，但只有 status 对应仍在等待的 snapshot、并实际改变 Leader progress 时，反转后的映射才构成可检查矛盾。strict 下 84 个 run 有标记、77 个失败；无 TLC 下 848 个有标记、750 个失败。这解释了它不是每个随机 seed 都必然失败。

restart 的激活标记只能在 `RawNode` 成功重建后发出；多数错误状态在重建过程中已经 panic，因此 79 个 strict runtime failure 和 813 个无 TLC runtime failure 没有机会记录该标记。成功重建并发出标记的 run 都被 `term_regressed` Oracle 捕获。无 TLC 的 5 个成功 run 没有走到能激活该 fault 的已提交 restart 场景。

## 稳定重现、回放与最小化

三个代表性失败 Plan 均连续完整执行三次并保持同一归一化失败签名。三条失败前 trace 各回放两次，所有记录步骤均逐步匹配：quorum 3/3、snapshot-status 19/19、restart 10/10。trace replay 只验证记录到失败前的 Runtime/SUT 确定性；它不会单独重新运行末尾的 TLC、Mapper 或 Oracle，因此“完整失败可重现”由三次 Plan 重跑验证。

| mutant | 原始 Action | 最小 Action | 稳定签名 | one-minimal |
|---|---:|---:|---|---|
| quorum | 3 | 3 | `model_failed / disabled_action / BecomeLeader` | 是 |
| snapshot-status | 19 | 17 | `mapping_failed / snapshot status progress mismatch` | 是 |
| restart | 11 | 10 | `runtime_failed / sut_panic / restart` | 是 |

最小化器分别执行 8、59、32 次尝试，并完成三次最终稳定性验证；均未触及尝试上限。这里的 one-minimal 表示删除任意一个剩余 Action 都不能保持同一失败签名，不保证在所有可能 Plan 中具有全局最短长度。

## 能说明什么，不能说明什么

本轮正面证明了当前 v1 基线能够在统一实验框架中检测三种不同位置的缺陷，并能保留、重现和缩减反例。零失败的 matched control 也说明这些注入没有在对应正常配置上造成直接误报。

它还不能证明系统已经覆盖所有 Raft 缺陷。restart 注入是明显不一致的存储状态，比只丢 vote、只丢 commit、日志落盘顺序错误或 snapshot/HardState 原子性破坏更容易发现；后续应增加这些较细粒度的恢复 mutant。snapshot-status 注入验证的是本项目观察映射的自洽性，不是上游 etcd-raft 的实现错误。每组 100 或 1,000 个 seed 也只给出当前策略、预算和配置下的经验检测率。

## 原始产物

主目录为 `runs/mutant-detection-20260724/`。`targeted/` 保存定向 A/B，`random/` 保存 12 组随机实验，`verification/` 保存重复运行、trace replay 和最小化结果；`status.log` 与 `orchestrator.log` 记录执行过程。该目录受 Git 忽略，不作为仓库长期接口；正式结论以本文档为准。
