# checkpoint v5 与 TLC 性能统计实验（2026-07-21）

## 修改内容

- 每条完成运行同步追加到 `runs.jsonl`，最终 `experiment-report.json` 和 checkpoint
  不再嵌入完整 Run 数组。
- checkpoint v5 保存 Corpus、候选调度、已完成索引、聚合计数、唯一状态及
  Plan/Trace/状态路径摘要集合和紧凑耗时样本。
- 恢复时先修复 JSONL 损坏尾行，再按 checkpoint 的 `run_summary_count` 截去已经
  写入但尚未提交的完整尾记录，对应候选随后确定性重跑。
- `progress.jsonl` 的 run completed 事件只记录 run index、candidate ID 和 Corpus ID。
- 严格 TLC 服务新增 `/metrics`，统计请求、模型事件、错误码以及事件映射、Action
  查询、后继计算、状态验证和序列化累计耗时。CLI 为初始执行和每次恢复分别保存
  start/end segment，因此服务重启或未提交的 SUT 执行不会被误算成完成运行。

## 100条等价性与体积实验

使用 `seed=470721`、100 runs、每条最多30个 PlanAction：

- 100/100成功，2674个 Action、2197个模型事件、327个唯一模型状态、47条 Corpus、
  68条唯一状态路径，与 checkpoint v4 实验完全一致；
- 排除实际耗时后，v4 内嵌 Run 与 v5 `runs.jsonl` 的逐运行内容完全一致，Corpus 文件
  字节一致；
- 实验目录从约2.2MB降到1.5MB；最终 report 从300KB降到28KB，progress 从400KB
  降到32KB，checkpoint 从904KB降到648KB；100条 `runs.jsonl` 为184KB。

本次 TLC 指标记录100个请求和2197个模型事件，其中累计映射约64.3ms、Action 查询
约8.6ms、后继计算约97.8ms、验证约75.1ms、状态序列化约91.6ms。

## 中断恢复实验

300 runs 实验在完成59条后发送 SIGINT。checkpoint 记录59条完成、1条 in-flight，
`runs.jsonl` 恰有59条。恢复后最终300/300成功，JSONL 包含0..299全部唯一 index，
seed 均满足 `base_seed + index`，最终 checkpoint 无 in-flight。

另一次120 runs 实验在50条后中断再恢复，TLC 指标文件保存两个 segment。服务端实际
处理121个请求，而最终只有120个已提交 Run；额外请求正是中断时已由 TLC 处理、但
Runner 未提交并在恢复后重跑的 in-flight 候选，说明模型成本和实验完成数被正确区分。
