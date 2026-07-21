# Checkpoint v6、反馈背压与 proposal drop 实验（2026-07-21）

## 背景与旧基线

两小时实验 `runs/tlc-ng-v5-soak-2h-20260721` 完成87000轮，其中86814成功、
186失败。全部失败都是 `MsgProp: raft proposal dropped`。该实验共执行2118897个
Action，强制 timeout 为568813（26.84%）；`message_not_available=384096`、
`timeout_term_bound=8556`、`model_bound_reached=8585`。Ready 候选约积压95000条。

旧 `checkpoint.json` 为951273959字节，`corpus.json` 为349707763字节。根因是
checkpoint 重复保存完整 Corpus Entry、运行汇总和无界 Ready Plan。

## 修改

- etcd-raft 直接或转发的 `ErrProposalDropped` 现在产生
  `raft.proposal_dropped` Effect，并作为模型 stutter；转发投递仍记录
  `raft.message_delivered`。该结果不再成为 `runtime_error`。
- checkpoint 升级为v6。Corpus 区只含排序后的 coverage keys 和 EntryCount；完整
  Entry 追加到 `corpus.jsonl`。恢复时 `runs.jsonl` 和 `corpus.jsonl` 都先按
  checkpoint 水位修复不完整尾行并截断孤儿记录。
- pending mutation 在 checkpoint 中只保存 EntryID、Count 和 Seed，不再内嵌
  Corpus Entry。最终 `corpus.json` 也是紧凑摘要。
- Ready 队列默认上限4096；满时确定性淘汰最旧候选。报告新增
  `admitted_mutations`、`discarded_mutations`、`peak_ready_candidates`。默认
  mutations-per-state 从2降为1，max-mutations-per-corpus 从8降为2。
- 随机策略的 Deliver/Timeout 默认权重从50/20调整为60/5；timeout cooldown 为4，
  已有 Leader 时 timeout 权重再降为四分之一。本地随机变异插入 timeout 的概率
  从25%降为10%。
- 非空链路上的越界 Start 会钳制到最后一条消息并记录
  `selector_start_clamped`；空链路仍记录 `message_not_available`。
- `run`/`experiment` 新增 `-largest-term`、`-max-log-index`。严格 TLC `/health`
  暴露实际 cfg 边界，CLI 自动拒绝与 Go Mapper/随机/LLM 边界不一致的服务；恢复
  禁止覆盖原边界。仓库提供明确的 `raft-5.cfg` 和 `raft-10.cfg`。

## 验证

执行了：

```text
gofmt
go test ./...
tools/tlc-server/test.sh
```

Go 全量测试和严格 TLC Java 集成测试均通过。

无 TLC 随机烟雾实验：

```text
runs/tlc-ng-v6-no-tlc-smoke-20260721
runs=200 seed=470721 succeeded=200 failed=0 actions=10000
timeout=420 (4.20%) proposal_dropped=30 runtime_error=0
```

相同 seed 的100条严格 TLC 实验（5/5烟雾边界）：

```text
runs/tlc-ng-v6-seed470721-100-20260721
runs=100 seed=470721 succeeded=100 failed=0
actions=4158 model_events=3597 unique_model_states=556 corpus=49
generated=95 admitted=94 discarded=1 executed_mutations=94
peak_ready_candidates=12 (configured maximum=16)
timeout=323 (7.77%)
proposal_dropped=76 runtime_error=0
message_not_available=790 selector_start_clamped=325
model_bound_reached=0 timeout_term_bound=0
checkpoint.json=86213 bytes
corpus.json=14143 bytes corpus.jsonl=173012 bytes
```

仓库已有的同 seed、同100条、同5/5边界 v5 对照
`runs/tlc-ng-checkpoint-v5-feedback-100` 为：2674 Actions、timeout=440（16.45%）、
`message_not_available=279`、327个唯一状态、47条Corpus，checkpoint=661341字节。
新策略因变异默认值、消息钳制和动作权重变化而执行了不同候选，不能要求逐轨迹相同；
timeout 占比降到7.77%（相对下降52.8%），checkpoint 降到86213字节（减少87.0%）。
`message_not_available` 升到790，同时325次旧的越界选择被明确归入
`selector_start_clamped`，应结合总Action和新原因码理解。两组100条实验的
`model_bound_reached` 都为0；新实验也没有 `timeout_term_bound`。

本次100条与旧87000条不是同规模 A/B，因此绝对计数不可直接比较；以动作占比观察，
强制 timeout 相对两小时 soak 从26.84%降到7.77%，下降19.07个百分点（相对下降约71%）。
proposal drop 已进入统计且没有任何 runtime failure。最终 checkpoint 不含 Ready Plan；
运行中的 checkpoint 也受到配置上限约束。

## 中断恢复与确定性

`runs/tlc-ng-v6-resume-20260721` 在第45条后由 `timeout` 发送 SIGTERM，进程按预期
以 `context canceled` 停止。当时 checkpoint v6 为约136KiB，Corpus/Run 水位分别
为29/45，Ready 为8且没有超过配置上限8。随后从同一目录恢复至120条：

```text
succeeded=120 failed=0 corpus=72 unique_model_states=713
actions=5191 generated=137 admitted=136 discarded=22 executed_mutations=115
peak_ready_candidates=8 checkpoint.json=102200 bytes
```

另跑未中断对照 `runs/tlc-ng-v6-resume-control-20260721`。两者：

- `corpus.jsonl` SHA-256 完全相同；
- Runs 按 index 排序并删除 duration 后 SHA-256 完全相同；
- 聚合统计删除 duration、吞吐、elapsed 和 timeline elapsed 后 SHA-256 完全相同。

这验证了相同 seed 的调度、Corpus、候选淘汰和恢复结果均确定性一致。

## TLC 10/10 边界说明

后续已完成 TLC Action 按需绑定，10/10服务启动 RSS 降至约84MiB，并通过
同 seed 的100条严格 TLC 实验。详见
[`lazy-tlc-actions-20260721.md`](lazy-tlc-actions-20260721.md)。
