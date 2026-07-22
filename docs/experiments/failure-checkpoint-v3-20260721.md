# 失败分类与 checkpoint v3 实验（2026-07-21）

## 修改内容

- 将 Profile 预检结果拆为 `inapplicable`、`model_bound_reached` 和真正的
  `unsupported_by_model`；前两类不再计入实验失败。
- Corpus 只保存 Plan、Concrete ActionSequence 和模型状态键，不再复制完整 Trace。
- Mutation 完成只追加生命周期 journal，不再强制写完整 checkpoint；LLM 统计仍独立
  原子保存。checkpoint 保留周期、正常结束和中断退出三类写入时机。
- 未完成的 Report 运行槽位使用紧凑 JSON；取消路径在返回和落盘前统一补齐 Corpus、
  retained runs 和聚合统计。checkpoint 格式升级为 v3。

## 验证结果

自动化检查全部通过：

```bash
go test ./...
go test -race ./...
go vet ./...
```

使用本机 controlled TLC 运行100条、每条最多100个 PlanAction 的真实 Raft 反馈实验：

- 100/100 成功，覆盖622个唯一模型状态，保留34个 Corpus 条目；
- 统计到1812次 `inapplicable`、1次 `model_bound_reached`，没有模型不支持、映射、
  Oracle 或 SUT 失败；
- checkpoint 约1.4MB，Corpus 约928KB，experiment report 约348KB；
- checkpoint 和 Corpus 均不含 `trace` 字段，完成态 v3 恢复后的全部汇总值一致。

另以 `runs=20000` 启动实验并在3秒后发送 SIGTERM：中断时完成42条、保留16个
Corpus，返回报告与 checkpoint 都记录 `completed=42`、`corpus=16`、`retained=16`，
checkpoint 约1.6MB。再次从该 checkpoint 恢复并中断时完成数从42增长到70，Corpus
从16增长到26，证明紧凑格式仍保存了完整调度恢复状态。
