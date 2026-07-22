# 唯一性统计与周期种子注入实验（2026-07-21）

## 实现范围

本轮增加三类稳定摘要和汇总统计：

- Plan 摘要只包含动作，忽略来源等 Metadata；
- Concrete Trace 摘要保留具体 Step、时间、Action、Effect 和节点快照，忽略
  ExecutionID、seed 和 Trace Metadata；
- 模型状态路径摘要保留 `State.Key` 的顺序和重复项，与只统计状态集合不同。

Report 和 `experiment-metrics.json` 会记录观察数、唯一数、重复率，并按
`random_init`、`random_mutation`、`periodic_random` 等来源归属首次发现贡献。
覆盖时间线也同步记录三类唯一数，便于比较达到同一多样性所需的执行次数和时间。

同时增加 `-random-seed-interval` 和 `-random-seeds-per-interval`。注入以实际
完成执行数为阈值，新随机种子优先取得空闲槽位，但不清空 ready mutation 队列。
checkpoint v2 保存下一阈值和待注入数量。

## 自动化验证

以下命令全部通过：

```bash
go test ./...
go test -race ./...
go vet ./...
```

核心调度测试连续运行20次仍稳定通过。测试覆盖：摘要身份字段排除、路径顺序、
重复率、来源归属、`initial → mutation → periodic → mutation → periodic` 调度顺序，
以及周期阈值跨 checkpoint 恢复。

## 真实 Raft 随机实验

第一组不连接 TLC，配置为20次执行、每次20个 Action、并发2、每完成5次注入1个
随机种子。结果为：

- 20次全部成功，共400个 Action；
- 初始/补充随机种子17次，周期种子3次；
- 唯一 Plan 20、唯一 Concrete Trace 20，未执行模型所以状态路径观察数为0；
- Report 与 metrics 的唯一数和周期种子数一致；
- 最终 checkpoint 为v2，`next_random_seed_at=25`、`random_seeds_due=0`。

第二组使用固定返回状态键1的受控模型替身，配置为8次执行、每完成2次注入1个
随机种子，并让首条 Corpus 生成2个本地变异。8次全部成功，实际候选顺序为：

```text
initial, mutation, periodic_random, mutation,
periodic_random, initial, periodic_random, initial
```

两个已经生成的 mutation 均被执行，没有被周期注入清除。模型状态路径被观察8次，
唯一1条，重复率为0.875；三个来源分别记录了3、2、3次执行，只有首个
`random_init` 发现该状态路径和模型状态。

## SIGTERM 恢复实验

使用每次模型请求延迟80毫秒的替身运行15次，在完成4次且第5次仍在执行时发送
SIGTERM。中断 checkpoint 保存 `next_run_index=5`、一个 in-flight 候选、
`next_random_seed_at=6` 和零个待注入种子。只用原目录恢复后完成全部15次：

- 周期种子位于 run 3、6、9、12，共4次，阈值没有重复或跳过；
- 两个 mutation 都得到执行；
- 最终 `next_random_seed_at=18`、`random_seeds_due=0`；
- 唯一模型状态路径仍为1，说明恢复保留了此前的唯一性历史。

其中一条本地随机 mutation 因向非 leader 发送 client request 被模型 Profile 拒绝。
这是既有随机变异允许生成无效语义组合的可观测结果，不是 checkpoint 或周期注入
造成的数据丢失；其余14次成功，失败轨迹和统计均被正常持久化。
