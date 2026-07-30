# C=2 成功 seed 的差分诊断

## 1. 结论

第五轮唯一成功个案是 Goal B、seed `4101`、realized-aware、Frontier 容量 `C=2`。本轮用相同代码、配置和 seed 完整重跑了 7 个配置，并保存了所有候选 Plan、Trace、Goal/Branch Progress 和 replay 结果。

最重要的结论不是“容量 2 天生最好”，而是：

1. `C=2` 恰好保留并连续选择了一条能够继续推进的前缀链；
2. `C=1` 过早只保留一条路径，虽然到达 W3，但之后停在同一阶段；
3. `C=4/C=8` 在固定 20 个候选预算内轮换了更多浅前缀，没有给有效链足够连续的变异次数；
4. `C=2` 所谓的“两个完整 Realized Branch”实际是两个 **Realized Branch 实例**，二者都属于 `goal-b-higher-term-vote`，不是两种不同语义 Branch；
5. 成功路径原本计划为 Heartbeat/MsgApp，但实际由 higher-term Vote 形成。这说明第五轮的 planned label 不能代替真实因果路径，也直接支持本轮引入 Partial Evidence 和 Commitment。

因此，这个个案能说明 Frontier 选择和预算连续性很重要，但单个 seed 不能证明 `C=2` 在统计上优于其他容量。

## 2. 输入与可复现性

接受的原始重跑目录：

`/tmp/modelfuzz-ng-round6-c2-differential-raw-v2-20260728`

离线差分产物：

`/tmp/modelfuzz-ng-round6-c2-analysis-v4-20260728`

其中：

- `c2-step-comparison.jsonl`：1409 条逐 Action 对齐记录；
- `c2-differential-summary.json`：各配置结果、首次 Action 分叉和最小成功骨架；
- seed 固定为 `4101`；
- 所有配置的 LLM 调用为 0；
- online/offline mismatch 为 0；
- prefix replay 为 96/96。

第一次尝试生成的 `/tmp/modelfuzz-ng-round6-c2-differential-raw-20260728` 因 manifest 的布尔默认继承问题只有 6 个不完整报告，明确排除，不进入分析。

## 3. 七个配置的结果

| 配置 | Goal | 首次成功候选 | 候选 | Action |
|---|---:|---:|---:|---:|
| weak operators-only | 否 | - | 20 | 410 |
| realized-aware C=1 | 否 | - | 20 | 219 |
| realized-aware C=2 | 是 | 17 | 17 | 168 |
| realized-aware C=4 | 否 | - | 20 | 191 |
| realized-aware C=8 | 否 | - | 20 | 190 |
| planned-only C=4 | 否 | - | 20 | 191 |
| strong C=1 | 是 | 5 | 5 | 40 |

strong C=1 仍然是能力上界，不应和 weak 模式混成同一结论。

## 4. 首次关键分叉

### 4.1 Frontier 决策先分叉

几个 weak Frontier 配置在 candidate 0～6 产生相同的候选 Plan。candidate 7 开始选择不同 parent：

- C=1 选择 `frontier-000000`；
- C=2 选择 `frontier-000001`；
- C=4/C=8 选择 `frontier-000000`。

此时具体 Action 仍可能相同，所以要区分“第一次 Frontier 决策不同”和“第一次实际 Action 不同”。

### 4.2 实际 Action 随后分叉

- C=2 与 C=1：candidate 8、Plan Action 10 首次不同。C=2 对节点 3 执行 Crash；C=1 投递 `m9`。
- C=2 与 realized-aware C=4/C=8：candidate 9、Plan Action 10 首次不同。C=2 投递 `m9`，C=4/C=8 Crash 节点 1。
- C=2 与 planned-only C=4：同样在 candidate 9 首次出现实际 Action 分叉。

MessageID 只用于定位原始 Trace，不进入 Evidence 语义 key。

## 5. C=2 的有效前缀链

C=2 后半段的 parent 链是：

`candidate 10 → 11 → 12/13 → 15 → 16`

关键变化为：

1. candidate 11 到达 W3；
2. candidate 13 保留了可继续推进的 W3 前缀；
3. candidate 15 从 `frontier-000013` Restart，直接到 W5，并形成第一个完整可判定的 Vote 路径实例；
4. candidate 16 选择 `frontier-000015`，投递 higher-term Vote，到达 W6/Goal。

最终 Frontier 中保留：

- `frontier-000016`：W6、distance 0、实际 Vote Branch；
- `frontier-000013`：W3、distance 2、尚未判定。

candidate 15 的成功前缀确实是 candidate 16 的直接 parent，所以它不是“只是并行存在”的无关 Branch。

## 6. 两个 Realized 实例究竟是什么

原始分布为：

`goal-b-higher-term-vote:63173ba2510c = 2`

两个实例分别是：

| candidate | Planned Branch | 实际 Branch | 是否到达 Goal |
|---:|---|---|---:|
| 15 | higher-term-heartbeat | higher-term-vote | 否，W5 |
| 16 | higher-term-msgapp | higher-term-vote | 是，W6 |

所以正确表述是“两个完整 Vote Branch 实例”，而不是“两个不同的完整 Branch”。planned/realized 偏差发生在 W5，这也是不能直接按 planned Branch 给预算的原因。

## 7. 最小成功 Branch 骨架

该骨架是因果摘要，不是可执行 Plan，也不会写入 mutation：

1. 形成 W1：唯一 Leader 和 TargetFollower 绑定；
2. TargetFollower 真实 Crash；
3. 目标离线期间 active 集群 term 真实推进；
4. TargetFollower Restart，但恢复尚未完成；
5. higher-term Vote 已进入目标队列；
6. 到达 Vote Branch Commitment；
7. 真实投递 higher-term Vote；
8. TargetFollower 发生预期的 term/role 更新，Goal 成立。

对应必要 Evidence：

- `target-crashed`；
- `active-election-started`；
- `active-term-advanced`；
- `target-restarted-incomplete`；
- `higher-term-message-pending`。

`higher-term-message-delivered` 是完整实现所需的后续 Evidence，但不属于“消息已经可生成”的 Commitment 条件。

## 8. 对 12 个诊断问题的回答

1. **第一次不同在哪里？** Frontier parent 在 candidate 7 先不同；实际 Action 对 C=1 在 candidate 8、对 C=4/C=8 在 candidate 9 不同。
2. **哪个 Action 导致差异？** Crash 与消息投递顺序不同；具体见逐步 JSONL。
3. **C=2 保留了哪条 prefix？** 最终成功链的关键是 candidate 15 的 W5 Vote 前缀。
4. **其他容量为何没有它？** 它们在此前已选择不同 parent，因此没有生成同一 candidate 15；不是生成后被简单淘汰。
5. **两个完整 Realized Branch 是什么？** 两个都是 Vote Branch 实例。
6. **哪个 Branch 贡献 Goal？** `goal-b-higher-term-vote`。
7. **另一个是否提供必要准备？** candidate 15 是 candidate 16 的直接 parent，提供了必要准备；但它不是另一种语义 Branch。
8. **C=4 是否预算分散？** 个案证据支持这一解释：20 个候选被分散到更多浅 parent；正式结论必须依赖多 seed 公平实验。
9. **C=8 是否保留过多弱 Seed？** C=8 最终保留 8 个 Seed，20 个候选内没有形成 W5 链，符合“轮换延缓”的现象，但单 seed 不能证明因果。
10. **progress guard 是否阻止有效 Seed？** 旧 artifact 没有逐次 guard 拒绝字段，不能确认；未发现“有效 Seed 已生成但被 guard 拒绝”的直接证据。
11. **semantic dedup 是否错误合并？** C=4/C=8 各有 10 次 dedup，但现有记录没有证明它合并了语义不同的有效路径。
12. **micro-progress 是否决定存活？** 第五轮 legacy micro-progress 参与了 prefix 边界；这个个案支持进一步审计，但不能单独证明所有 micro-progress 都有用。本轮因此只让登记为必要的 Evidence 延长新 Frontier 前缀。

## 9. 本轮设计影响

该分析直接导致三项设计选择：

1. 明确区分 Planned、Supported、Committed、RealizedDecidable 和 Full Realized；
2. Evidence key 不使用 MessageID、绝对 term/index 或最终 Goal 结果；
3. 阶段预算依据当前前缀的 supported Evidence/Commitment 分配，不依据最终成功率在线学习。
