# Branch / Evidence 冻结说明

日期：2026-07-29
状态：冻结；保留兼容性，不再作为默认在线搜索主线。

## 1. 已实现能力

第五、六轮已经实现人工 Behavior Branch、计划路径与实际路径对照、Deviation、Partial Evidence、Commitment、Evidence Frontier、阶段预算、Failure-to-Form、在线/离线重算和相应 JSON/CSV artifact。现有 schema 与 CLI 保留，历史 artifact 继续可读。

## 2. 已验证的价值

Branch / Evidence 最可靠的价值是诊断：它能解释一个候选为什么没有形成预期因果路径，区分“计划过某条路径”和“真实消息证据已经形成”，并帮助定位 Goal A 的多数派推进/压缩边界问题和 Goal B 的选举未完成问题。它也适合作为未来 LLM 的结构化输入候选，但本轮没有调用 LLM。

## 3. 未验证的价值

第五轮多分支 Frontier 没有稳定提高 Goal reach；第六轮 Evidence/阶段预算对 Goal A 仍为 0/10，对 Goal B 有局部正面结果但不稳定超过普通 Standard。Snapshot mutant 的弱 Evidence 方法仍是 0/5。现有证据不支持把 Branch / Evidence 作为默认在线排序、预算或变异依据。

## 4. 第五、六轮正负结果

- 正面：路径偏差、关键消息和选举完成度变得可解释；Restart mutant 检出由 Standard 4/5 到 Evidence 5/5。
- 负面：Goal A 的 weak 方法没有突破；增加分支容量或 Evidence 层级没有自动修复候选生成；复杂度和实验变量显著增加。
- 结论：问题主要在“局部候选怎么形成”，不是缺少更多搜索层级。

## 5. 第七轮 record-only 验证

本轮 M2 与 M3 使用相同 seed：

- M2：Weak Standard Frontier C=1 + focused mutation；
- M3：M2 + Branch / Evidence record-only。

两个 Goal 共 20 对运行，目标结果、最终 Plan key、精确 Trace key、语义 Trace key、Goal progress path 和 Facet path 的不一致数都是 0。M2/M3 的成功率分别完全相同：Goal A 9/10，Goal B 10/10。M3 只增加分析时间，未改变候选、Frontier、预算、prefix 或 RNG。

这说明 record-only 边界可行，也说明 focused mutation 的收益不能归因给 Branch / Evidence。

## 6. 冻结后的用途

Branch / Evidence 只保留为：

- 诊断和 Failure-to-Form 解释；
- 可重算 artifact；
- 未来 LLM 的结构化输入候选；
- 非默认、显式开启的实验功能。

不再新增 Branch、Evidence、Commitment、Utility、Stage Budget 或 Evidence-aware Frontier 层级。

## 7. 默认论文主线

默认论文主线不采用 Branch / Evidence 在线引导。当前证据支持：

`Facet 广度反馈 + Waypoint Frontier 深度搜索 + 协议专用局部 Mutation`

Branch / Evidence 放在相关设计探索、诊断工具或消融附录中。

## 8. Experimental 代码

以下能力保持 experimental：

- `diversity-aware-frontier`；
- `evidence-aware-frontier`；
- `stage-budgeted`；
- Branch commitment 与 evidence utility；
- Failure-to-Form 的 Branch 特定分类。

不删除代码，避免破坏历史实验和 artifact。

## 9. 冻结的 CLI 与 schema

冻结并保持兼容：

- `branch-templates`、`all-feasible-branches`、`branch-awareness`；
- `branch-evidence-mode`、`branch-frontier-mode`、`branch-budget-mode`；
- `branch-*-quota`、`micro-progress-policy`、`formation-failure-report`；
- `raft-behavior-branches-v1-prototype`；
- `raft-branch-evidence-v1-prototype`。

第七轮只增加 `branch-evidence-record-only`，用于证明记录与搜索决策隔离；它不改变旧 schema。

## 10. 最终决定

Branch / Evidence 已正式冻结。除兼容性修复、明确 bug 修复和重算工具维护外，不再继续扩展。下一轮不能以“再加一层抽象”作为默认方向。
