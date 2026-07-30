# Focused Protocol-Aware Mutation 与方法冻结

日期：2026-07-29
结论类型：Focused Mutation 明显有效，但仍存在路径集中和强方法速度更快的限制。

## 1. 本轮动机

第六轮已经说明，仅增加 Branch、Evidence 或阶段预算不能稳定修复弱搜索。真正的缺口是：普通弱变异知道“大致该做哪类动作”，却不能把几个互相依赖的 Raft 动作组织成一个 Standard Frontier 能保留的局部进展。本轮只解决这个候选生成问题。

## 2. 第六轮结论

Goal A 的 weak Standard、Diversity、Evidence 方法均为 0/10，Strong 为 10/10；Goal B 的 Standard 为 7/10，其他弱方法结果不稳定，Strong 为 10/10。Snapshot mutant 弱方法 0/5，Restart mutant Standard 4/5。Prefix replay 和 Online/Offline 重算稳定。

## 3. 为什么停止扩展 Branch / Evidence

失败分析已经足够清楚：Goal A 缺少隔离目标后的多数派复制闭环，Goal B 会在真实投票完成前重启目标。继续增加搜索层级不能生成缺少的协议事件。本轮因此冻结 Branch / Evidence，只在 M3 中做 record-only 一致性验证。

## 4. Advisor 通用接口

`internal/protocolmutation` 定义协议无关的 `Advisor`：

- 输入只有 Goal ID、当前 Waypoint、当前 Observation、语义角色、合法动作、候选编号和局部无进展计数；
- 输出有限的局部候选、权重、前置条件、Reason、预期效果、当前队列 MessageID 审计值和稳定 key；
- 不接收未来 Trace、成功 Plan、mutant failure Plan 或最终结果；
- Plan 仍使用“链路 + 当前位置”消息选择器，当前 MessageID 只写审计记录；
- 运行结束后再回填 ActualEffect 和 LocalProgress。

不同协议可以实现同一接口替换 Raft Advisor；Frontier、Evaluator、Replay、Runtime 和持久化无需知道协议消息类型。

## 5. Raft 专用实现边界

`internal/protocolmutation/raft` 解释 Raft 的 role、term、last term/index、leader progress、MsgApp、MsgAppResp、MsgVote、MsgVoteResp 和 snapshot/compaction 边界。通用 Frontier 没有新增 Raft 消息判断。

## 6. Goal A operator

`Majority-Progress-With-Isolated-Target` 先按语义角色选择 TargetFollower，隔离目标，然后在活动多数派内运行有限复制窗口。它不选择最终 MsgSnap，也不负责完成 W5–W7。

## 7. Goal A 局部阶段

- A0：没有稳定 Leader，推进真实选举；
- A1：隔离当前绑定的 TargetFollower；
- A2：优先消费多数派 MsgApp/MsgAppResp；
- A3/A4：有限请求、复制、响应，增加 leader/target lag；
- A5：Observation 表明已跨压缩边界或 pending snapshot，停止专用推进；
- A6：交还普通 Weak Goal/Standard Frontier，后者负责 heal、MsgSnap 和安装。

每次最多 9 个相邻动作。这个窗口不是完整 Plan，而是为跨过 Standard Frontier 不保留“无 Waypoint/距离变化中间态”的限制。

## 8. Goal A 消息选择

只选择当前队列中未阻塞、位于活动多数派内部的真实 MsgApp/MsgAppResp。后续响应使用反向链路范围选择器，不预知未来 MessageID。Target 链路由真实 partition 阻塞；Advisor 不直接修改日志、term、role 或 next/match。

## 9. Goal B operator

`Active-Subset-Election-Completion` 崩溃 Target，选择活动 follower，必要时先完成日志追赶，再触发真实 timeout；随后按当前队列因果顺序交付 Vote/Resp，形成 Leader 后才重启 Target。最终 higher-term 消息仍由普通 Goal 逻辑选择。

## 10. Goal B 局部阶段

- B0：建立稳定 Leader；
- B1：Crash Target；
- B2：选择活动候选并检查日志新鲜度；
- B3：真实 timeout；
- B4：真实 Vote/VoteResp；
- B5：出现更高 term 的真实活动 Leader 后 Restart；
- B6：交还普通 Weak Goal/Frontier，完成最终 higher-term 消息。

## 11. Goal B 候选选择

候选必须运行、不是 Target，且优先不是当前 Leader。默认按 `(lastTerm,lastIndex)` 选择；如候选尚有真实 MsgApp 待交付，则局部窗口先交付 MsgApp/Resp，再 timeout。没有固定 Candidate NodeID。

## 12. 防止硬编码

审计结论：

1. 有有限阶段模板，但没有完整固定成功 Plan；
2. 节点来自 Observation 和 Goal binding，没有固定 ID；
3. 不读取最终 MsgSnap/higher-term MessageID；
4. 接口没有未来 Trace 字段；
5. 未读取成功 seed Plan；
6. 未读取 mutant failure Plan；
7. 所有消息由真实 etcd-raft 生成；
8. 不直接修改 term/log/role；
9. 所有动作经过 Plan Resolver、Runtime、Mapper、TLC 和 Oracle；
10. Goal 是否成功仍只由冻结 Evaluator 判断；
11. Pilot/正式实验使用多个 seed；
12. Pilot 用 threshold=3，开发小 Trace 用 threshold=4，未只绑定一个阈值。

风险仍在：focused 的相对语义 Trace 数明显少于 legacy，说明局部模板造成路径集中。它是可解释的协议插件，不等于通用自动规划器。

## 13. Pilot

5 个严格 seed、candidate budget=30、Standard C=1、weak、strict TLC、replay：

| Goal | legacy | focused | focused 首次命中候选 |
|---|---:|---:|---:|
| A | 0/5 | 5/5 | 12–21 |
| B | 5/5 | 5/5 | 5–9 |

冻结参数：priority=16、local action cap=9、no-progress cap=8、queue limit=64、每次 request 值为一条有限请求。

## 14. 正式方法矩阵

每个 Goal 使用相同 10 seed：

- M0 Weak operators-only legacy；
- M1 Weak Standard C=1 legacy；
- M2 Weak Standard C=1 + focused；
- M3 M2 + Branch/Evidence record-only；
- M4 Strong C=1。

统一 candidate=30、action=4500、max PlanAction=180、3 节点、snapshot threshold=3、retain=1、worker=1、strict TLC、Oracle 和 replay。

## 15. Goal reach

| Goal | M0 | M1 | M2 | M3 | M4 |
|---|---:|---:|---:|---:|---:|
| A | 0/10 | 0/10 | 9/10 | 9/10 | 10/10 |
| B | 1/10 | 5/10 | 10/10 | 10/10 | 10/10 |

这满足“Focused Mutation 明显有效”。它没有超过 Strong 上界，但把弱 Standard 的主要生成缺口补上。

## 16. Waypoint reach

平均到达 Waypoint 数：

| Goal | M0 | M1 | M2 | M3 | M4 |
|---|---:|---:|---:|---:|---:|
| A（共7） | 1.2 | 1.3 | 6.8 | 6.8 | 7.0 |
| B（共6） | 2.7 | 5.0 | 6.0 | 6.0 | 6.0 |

Goal A 的 W3/W4 从偶发或不可达变为稳定可达；Goal B 的 W3–W5 形成真实选举闭环。

## 17. candidates / actions / time

正式 10 seed 聚合：

| Goal/方法 | 命中 | 首次命中候选中位数 | 总 Actions | 墙钟 ms |
|---|---:|---:|---:|---:|
| A M1 | 0/10 | — | 1,912 | 18,755 |
| A M2 | 9/10 | 19 | 3,673 | 42,331 |
| A M4 | 10/10 | 8 | 1,050 | 13,491 |
| B M1 | 5/10 | 19 | 1,474 | 14,996 |
| B M2 | 10/10 | 9 | 1,218 | 12,935 |
| B M4 | 10/10 | 5 | 400 | 4,441 |

Goal A focused 用更多单候选动作换取从 0 到 9 次成功；Goal B 同时提高成功率并降低候选成本。Strong 仍更快，不能宣称 focused 已达到强知识上界。

## 18. Failure-to-Form 变化

公平矩阵没有选择 Branch，也没有打开 Branch 特定 FormationFailure，因此不能给出同口径类别计数。可验证替代信号是：

- A legacy 最常停在 W2，focused 平均到 6.8/7；
- B legacy 只有 5/10 完成，focused 10/10；
- 局部审计显示 A 形成真实多数派复制窗口，B 形成真实活动子集选举。

因此可说 `lag-insufficient`、`compaction-boundary-not-crossed` 和 `election-not-completed` 的对应现象减少；不能伪造为未实际记录的 Failure-to-Form 精确比例。

## 19. Goal A 消融

每组 5 个配对 seed。Full 为 4/5；No-Boundary 为 2/5；No-Target-Suppression 为 4/5。最初 No-Quorum 实验错误地仍从 request 宏中携带复制动作，得到无效的 4/5；修正并增加单元测试后独立复跑为 0/5，且每个 seed 只到 W2。

结论：多数派维护和边界识别是实质组件；Target suppression 在当前真实 partition 下被网络层覆盖，消融无差异。

## 20. Goal B 消融

| 变体 | 命中 |
|---|---:|
| Full | 5/5 |
| No-Log-Freshness | 5/5 |
| No-Vote-Completion | 4/5 |
| Early-Restart | 4/5 |

No-Log-Freshness 甚至更快，说明同质三节点初始日志不足以证明日志检查有收益。Vote completion 和延迟 restart 有正面信号，但 5 seed 不能估计精确效应大小。

## 21. Snapshot mutant

5 个配对 seed：

| 方法 | 检出 |
|---|---:|
| legacy Standard | 0/5 |
| focused Standard | 4/5 |
| Strong | 5/5 |

focused 的失败均为 `mapping_failed`，首次失败候选为 13–18。它显著改善 weak 检出，但仍没有达到 Strong。

## 22. Restart mutant

三种方法都是 5/5。focused 每个 seed 都在第 4 候选、36 个累计 Action 检出 `raft.basic:term_regressed`；legacy 首次失败候选为 6–23，中位约 13；Strong 在第 4 候选、32 Action。

## 23. control false positive

Snapshot/Restart × legacy/focused/strong 共 30 条 control，bug detection 为 0/30，Runtime/TLC/Oracle 未报告假阳性。

## 24. replay 与 ddmin

- 正式与消融/Mutant 前缀 replay 全部成功，prefix execution mismatch=0；
- focused Snapshot 代表失败 Trace 独立 replay 3/3，每次 21/21 step；
- focused Restart 代表失败 Trace 独立 replay 3/3，每次 12/12 step；
- Snapshot ddmin：17→13 PlanAction，42 次尝试，最终稳定 3/3，one-minimal；
- Restart ddmin：11→4 PlanAction，30 次尝试，最终稳定 3/3，one-minimal。

Trace replay 证明失败前具体执行确定；完整 failure 重现由 minimizer 的独立重跑证明。

## 25. Online / Offline

Pilot、100 条正式、40 条消融和 60 条 mutant/control 的 Online/Offline mismatch 都是 0。M2/M3 正式前缀 replay 分别逐 seed一致。

## 26. Trace 与语义路径多样性

所有正式 campaign 的 10 个 seed 都产生 10 个不同精确 Trace。相对语义 Trace：

- A M1=7，M2=2，M4=1；
- B M1=5，M2=1，M4=1。

Goal progress path：A M2 为 10 类；B M2 为 6 类。focused 没有退化为同一个精确 Trace，但语义调度明显集中。这是成功率提升的代价和当前限制。

## 27. 通用 / 协议专用代码规模

本轮通用接口位于 `internal/protocolmutation/advisor.go`，288 行（含汇总逻辑）；Raft 实现 647 行。测试代码单独统计。行数应以当前版本的 `wc -l` 复算。

## 28. 核心模块修改量

修改：

- `internal/goalsearch/mutation.go`：只增加 Advisor 调用与局部动作拼接；
- `cmd/modelfuzz-ng/goal_search.go`：CLI、记录、汇总 artifact；
- `cmd/modelfuzz-ng/goal_benchmark.go`：manifest 继承和命令透传。

没有修改 Goal predicate、Waypoint、Distance、Facet、Corpus admission、Runtime、TLC 模型、Oracle 或 Standard Frontier 排序。通用 search/frontier 中没有新增 Raft 消息类型判断。

## 29. Branch / Evidence 冻结

M2/M3 共 20 对 seed 的目标结果、Plan/Trace/语义 Trace/Goal path/Facet path mismatch=0。Branch / Evidence 正式冻结为诊断、artifact、未来 LLM 输入候选和非默认实验功能；不进入默认论文主线。

## 30. Facet / Waypoint 主线收敛

当前推荐：

`Facet 广度反馈 + Waypoint Frontier 深度搜索 + Protocol-Aware Local Mutation`

Facet 继续作为广度主线；Waypoint 保留深度进展；focused mutation 作为协议专用局部生成组件。停止实现 Goal-local/global Stall。

## 31. 负结果

- Goal A focused 正式仍有 1/10 未命中；
- Strong 在两个 Goal 上仍更快；
- focused 语义路径更集中；
- A Target suppression 消融无差异；
- B Log freshness 消融无收益；
- B 两个关键消融只有 1/5 的差异，证据较弱；
- Failure-to-Form 公平矩阵没有精确类别计数；
- 尚未在 5 节点、不同日志不对称初态或第二协议验证。

## 32. Artifact 与复现

主要目录：

- Pilot：`/tmp/modelfuzz-ng-round7-focused-pilot-v1-20260729`
- 正式：`/tmp/modelfuzz-ng-round7-focused-formal-v1-20260729`
- 消融：`/tmp/modelfuzz-ng-round7-focused-ablations-v1-20260729`
- No-Quorum 修正：`/tmp/modelfuzz-ng-round7-no-quorum-corrected-v1-20260729`
- mutant/control：`/tmp/modelfuzz-ng-round7-focused-mutants-v1-20260729`
- ddmin：`/tmp/modelfuzz-ng-round7-ddmin-{snapshot,restart}-v1`

每次 Goal Search 包含 settings、原始 Advisor JSONL、可重算 summary、stage/reason CSV、protocol coupling、freeze JSON、标准运行 artifact 和 figure-ready CSV。

## 33. 当前限制

只实现 etcd-raft、三节点、固定两个人工 Goal；不覆盖 membership、PreVote、CheckQuorum、真实 WAL/fsync/磁盘、外部进程或真实网络。Advisor 是人工协议知识，不是 LLM。人工 mutant 不是生产 bug。20/10/5 seed 的结果不能外推为未知生产缺陷发现概率。

## 34. 下一阶段建议

先停止新增抽象和 Advisor 阶段，冻结本轮方法与实验。下一阶段优先做论文实验冻结和广度主线验证：同预算比较 Raw/v2/Facet，并评估 Facet 接入 Corpus；随后用五节点和更多共识 Goal 检查 focused 的路径集中问题。LLM 暂缓到这些非 LLM 基线冻结后，再让 LLM 处理“跨 Goal/跨协议、代码难以枚举的候选解释与局部策略选择”，不能替代本轮可直接编码的 Raft Advisor。
