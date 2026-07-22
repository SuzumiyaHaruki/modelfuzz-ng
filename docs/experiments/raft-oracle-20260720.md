# 基础 Raft 在线 Oracle 实验（2026-07-20）

## 检查范围

Oracle 在每个真实 Raft Concrete Action 完成后检查：

- 同一节点、同一 epoch 内 term、commit 和 applied 不得回退；
- `applied <= commit <= last_index`；
- 一条执行历史中，同一 term 不能出现两个不同 leader；
- 任意两个具有完整前缀的节点，在 `min(commitA, commitB)` 处的日志摘要必须一致；
- 声明 committed-prefix 可用的节点，必须提供 Oracle 当前需要的比较检查点。

Oracle 不再直接比较包含未提交尾部的 `log_digest`。Adapter 从完整持久日志前缀
生成累积摘要，Observation 只暴露当前集群 commit 索引形成的有限检查点。
这样既能检查 commit 进度不同的节点，又不会因合法的未提交尾部产生误报。

## 正向端到端实验

四条 Plan 均经过真实 etcd-raft、在线 Oracle、Raft Mapper 和 controlled TLC：

| Plan | Action | 模型事件 | TLC状态 | Oracle Finding | 结果 |
|---|---:|---:|---:|---:|---|
| `election-commit-node1` | 6 | 9 | 9 | 0 | completed |
| `election-commit-node2` | 6 | 9 | 9 | 0 | completed |
| `client-request-commit` | 9 | 13 | 12 | 0 | completed |
| `follower-catchup-multi-entry` | 11 | 17 | 16 | 0 | completed |

这同时验证了 Oracle 不会把正常选举、单 entry 复制、commit 传播或多 entry
follower 追赶误判为违规。

## 故障注入实验

通过构造 Before/After Observation，分别注入：

- term 和 commit 回退；
- applied 大于 commit；
- leader 退出后，同一 term 的另一个节点再次成为 leader；
- 两个节点在共同 committed index 上具有不同的前缀摘要；
- 节点声明 committed-prefix 可用，但缺少必需的索引摘要。

上述违规均被预期的稳定 code 捕获。另有反例测试：当一个节点存在未提交日志
尾部时，即使完整日志摘要不同也不报告冲突。Engine 集成测试注入一个失败
Oracle，确认违规步骤的 Action、Trace、模型事件和 `step=1` Finding 均已保存，
且不会继续调用 TLC。

本地完整产物位于 `runs/oracle-positive-20260720/`，该目录按约定不提交 Git。
