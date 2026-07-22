# Panic 捕获与 committed-prefix 实验（2026-07-20）

## SUT panic 故障注入

在 Runtime 的 `Reset` 和 `Tick` 边界分别注入 panic，并在 Engine 层再执行一次
`AdvanceTicks` 故障轨迹。验证结果：

- panic 被转换为可用 `errors.Is` 识别的 `ErrSUTPanic`；
- `FailureRecord` 包含 operation、逻辑时间、Action、执行前 Observation、panic 值和堆栈；
- Runtime 终止后不再接受 Action，失败 Action 不进入 `Trace.Steps`；
- Engine 返回 `runtime_failed`，并在 Result 中保留已成功的 Trace 前缀和 FailureRecord；
- CLI 将失败记录独立写入 `failure.json`，同时内嵌在 `result.json`。

当前捕获边界是 Runtime 同步调用的 Adapter/SUT 方法。SUT 自行创建的后台
goroutine 若在边界外 panic，仍需要 Adapter 在该 goroutine 内转换成可上报故障。

## committed-prefix 正向实验

四条完整 Plan 均经过真实 etcd-raft、Mapper 和在线 Raft Oracle：

| Plan | Action | Effect | 模型事件 | Oracle Finding | 结果 |
|---|---:|---:|---:|---:|---|
| `election-commit-node1` | 6 | 16 | 9 | 0 | completed |
| `election-commit-node2` | 6 | 16 | 9 | 0 | completed |
| `client-request-commit` | 9 | 22 | 13 | 0 | completed |
| `follower-catchup-multi-entry` | 11 | 26 | 17 | 0 | completed |

多 entry 轨迹在一个过渡状态中出现节点 `commit=4` 和 `commit=1`。commit=4
节点同时暴露索引 1 和 4 的前缀摘要，Oracle 在索引 1 上成功比较共同
已提交前缀。最终 commit=4 的两个节点摘要一致。

故障单元测试额外覆盖了：共同前缀冲突、合法的不同未提交尾部、
commit 进度不同、crashed 节点，以及声明可用但缺少比较检查点的非法观测。

## Trace 与重放

committed-prefix 改变了 ObservationDigest，因此新轨迹版本升为 v4。上述四条
v4 轨迹严格重放分别匹配 `6/6`、`6/6`、`9/9` 和 `11/11` 步。此外，
一条改动前真实保存的 v3 多 entry 轨迹也成功匹配 `11/11`，确认了旧版
节点语义和摘要的重放兼容边界。

完整产物位于本地 `runs/panic-prefix-20260720/` 和
`runs/panic-prefix-replay-20260720/`，这些目录按约定不提交 Git。
