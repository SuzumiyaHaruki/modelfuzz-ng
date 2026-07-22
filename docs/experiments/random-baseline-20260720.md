# 随机策略与批量 Runner 实验（2026-07-20）

## 本地并发与确定性

使用 seed `1000..1019`，每次最多 30 条在线 PlanAction，4 路并行执行真实
etcd-raft：

| 运行数 | 完成 | Action | Effect | 模型事件 | Oracle Finding |
|---:|---:|---:|---:|---:|---:|
| 20 | 20 | 600 | 1031 | 551 | 0 |

动作分布为：deliver 320、drop 38、duplicate 31、timeout 155、request 25、
advance_ticks 31。其中 126 条消息动作选择了 `start>0`，证明策略实际覆盖了
非 FIFO 投递/丢弃/复制，而不只是保留了接口能力。

随后使用同一 seed 范围和参数改为单线程完整重跑。逐个 seed 对
`plan.json`、`actions.json`、`trace.json` 做字节比较，20/20 全部相同；批量汇总
也仍为 600 Action、1031 Effect、551 模型事件。这验证了运行结果由 seed 和状态
决定，不受 worker 调度影响。

## Controlled TLC 实验

使用 seed `2000..2009`、每次 20 条动作、串行连接 controlled TLC：

| 运行数 | 完成 | Action | Effect | 模型事件 | 跨运行唯一TLC状态 | Oracle Finding |
|---:|---:|---:|---:|---:|---:|---:|
| 10 | 10 | 200 | 364 | 193 | 138 | 0 |

10 条随机轨迹全部被真实 Raft、Mapper、Oracle 和 TLC 接受，没有 resolution、
runtime、mapping 或 model failure。每次运行使用独立 Runtime 和派生 seed；旧
controlled TLC 的请求隔离能力尚未确认，因此 CLI 在连接 TLC 时强制串行。

完整产物位于本地 `runs/random-baseline-*-20260720/`，不提交 Git。
