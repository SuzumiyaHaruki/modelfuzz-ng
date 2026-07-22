# 统计与可恢复持久化实验（2026-07-21）

## 实现范围

本轮增加了协议无关的 Action、Effect、出站消息类型、Resolution、模型事件、Oracle、timer、失败、
终止原因、消息队列和耗时统计；同时增加 `progress.jsonl`、原子
`checkpoint.json`、配置指纹、四种逐运行产物策略以及 `-resume`。

checkpoint 保存部分 Report、Corpus、ready 候选、in-flight 候选、pending mutation、
候选编号、run index、事件序号和累计运行时间。中断时尚未完成的执行和变异会按原
seed 重新提交。

当前 checkpoint v3 中 Corpus 只保存 Plan 和 Concrete ActionSequence，不再复制完整
Trace；Mutation 完成仍写入 `progress.jsonl`，但不会强制重写完整 checkpoint。周期
checkpoint、正常结束和中断退出仍保存可恢复快照。固定 Runs 数组中尚未执行的槽位
编码为 `{}`，因此实验早期的 checkpoint 大小不再与预设总运行数成大常数增长。

## 自动化验证

以下命令全部通过：

```bash
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖 Corpus 快照恢复、统计分位数、原子 JSON、损坏 JSONL 尾记录修复、
Runner 中断恢复、pending mutation 恢复、产物策略和 CLI 完成态恢复。

## 100 条真实 Raft 随机轨迹

配置为 `runs=100`、`max-plan-actions=30`、`parallelism=4`、
`artifact-policy=retained`、`checkpoint-every=7`，不连接 TLC。

- 100 条全部成功，共执行 3000 个 Action；
- 统计到 2836 个模型事件；
- Action 分布包含 Deliver 1530、Timeout 626、AdvanceTime 169、Crash 132、
  Restart 100、Drop 151、Duplicate 144 和 Request 148；
- Effect 分布包含 SendMessage 2493、TimerFired 689、ModelEvent 1640；
- 单条执行耗时 p50 为 24477 微秒、p95 为 45251 微秒、p99 为 51985 微秒；
- 覆盖曲线包含 100 个点，checkpoint 最终完成数为100；
- journal 共102条连续事件；因为没有 TLC 覆盖和失败，`retained` 策略没有保存
  `run-*` 目录，验证了精简产物策略。

## SIGTERM 后恢复实验

使用一个每次模型请求延迟70毫秒的受控测试服务运行12条真实 Raft 轨迹，在第3条
仍在执行时发送 SIGTERM。中断后的 checkpoint 状态为：

- 已完成2条；
- `next_run_index=3`；
- run 2 位于 in-flight；
- Corpus 已保留1条轨迹。

随后只指定原实验目录执行 `experiment -resume`。恢复结果为：

- 12条全部成功，最终 checkpoint 完成数为12且没有残留 in-flight；
- Corpus 为1，执行了2条 Mutation；
- journal 中 `run_completed` 的 index 恰好为 `0..11`，没有缺失或重复；
- journal sequence 严格连续；
- 覆盖曲线为12个点，`all` 策略保存了12个完整运行目录。

该实验验证的是进程级中断与恢复；断电时文件系统本身无法保证已确认写入介质的程度，
但 JSONL 会修复不完整尾记录，checkpoint 的临时文件不会替换最后一个完整快照。

最后使用 `summary` 策略完成20条、每条20个 Action 的烟雾实验，并只用
`experiment -resume <dir>` 重新打开完成态检查点。结果仍为20条、400个 Action、
零个 `run-*` 目录；出站消息类型统计为 MsgApp 62、MsgAppResp 20、
MsgHeartbeat 12、MsgVote 198、MsgVoteResp 80，说明恢复会自动沿用原来的
artifact/checkpoint 设置，消息类型统计也已经写入最终 metrics。
