# Raft 模型

该目录保存 ModelFuzz-NG 首个可运行的 Raft 模型。它沿用原 ModelFuzz
`raft_alt.tla` 的核心思路：网络队列由 Runtime 控制，TLA+ 模型本身不维护
message bag；实际消息投递由 `internal/model/raft.Mapper` 转成具体消息处理动作。

当前模型覆盖选举、成为 leader、客户端请求、日志复制和 commit index 推进，
可以支持第一条端到端轨迹：

```text
Timeout
  -> Deliver MsgVote
  -> Deliver MsgVoteResp
  -> BecomeLeader
  -> ClientRequest(leader no-op)
```

## 使用约束

- 默认配置是 3 节点，term、日志长度和请求值均有界；规模变化时应从同一份
  实验配置生成 `raft.cfg`、Adapter Config 和 Mapper Config。当前 Mapper 对应
  字段为 `NodeIDs`、`MaxValue`、`MaxLogIndex`、`LargestTerm`。
- 客户端请求暂时必须是 `1..MaxValue` 的十进制整数。`0` 保留给 etcd-raft
  成为 leader 时产生的 no-op entry。
- 模型处理 `MsgVote`、`MsgVoteResp`、`MsgApp`、`MsgAppResp`。`MsgHeartbeat`
  被显式抽象为无 entry 的 `MsgApp`，从而保留 term、角色降级和 commit 传播；
  当前配置下确实不改变模型状态的响应/只读消息被显式标记为 stutter。
- 一次 `MsgApp` 当前只支持零条或一条 entry；多 entry、未知消息、snapshot、
  membership change 等超出轻量模型表达能力的转换会返回错误，不会静默忽略。
- 首版尚未建模 crash/restart、snapshot、membership change 和 PreVote；Mapper
  会明确拒绝当前模型不支持的 crash/restart 转换。

`raft.tla` 的动作名和参数保持与原 controlled TLC 的 `RaftActionMapper` 兼容，
启动 TLC HTTP 服务时将本目录作为模型目录，并选择 `raft.tla`/`raft.cfg`。
