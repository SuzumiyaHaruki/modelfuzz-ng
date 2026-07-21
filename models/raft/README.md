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
  成为 leader 时产生的 no-op entry。Follower 已知当前 Leader 时会产生受控
  `MsgProp`；只有该消息投递到当前 Leader 时，Mapper 才生成 `ClientRequest`。
  Candidate 或无已知 Leader 的 Follower 丢弃 proposal，并明确映射为 stutter。
- 模型处理 `MsgVote`、`MsgVoteResp`、`MsgApp`、`MsgAppResp`。`MsgHeartbeat`
  被显式抽象为无 entry 的 `MsgApp`，从而保留 term、角色降级和 commit 传播；
  当前配置下确实不改变模型状态的响应/只读消息被显式标记为 stutter。
- controlled TLC 的旧 `RaftActionMapper` 一次只接受一条 entry。NG Mapper 会先
  根据真实 `MsgAppResp` 判断原子批次是否成功：成功的多 entry `MsgApp` 按日志
  顺序展开为多个单 entry 模型事件；被拒绝的批次只映射前缀不匹配动作，避免
  后续 entry 被模型错误接受。展开产生的中间状态只存在于模型侧，不对应额外
  的 SUT Action。
- 更高term节点可能直接忽略旧term的多 entry `MsgApp` 且不发送响应。Mapper
  将这种节点状态未变化的输入缩减为第一条 entry 对应的模型 stutter，不再把
  “没有响应”误判成映射失败。
- 未知消息、snapshot、membership change 等超出轻量模型表达能力的转换会返回
  错误，不会静默忽略。
- 模型使用 `currentActive`、`RemoveFromActive` 和 `AddToActive` 表达
  crash/restart；崩溃保留稳定状态，恢复会重置节点的 Raft 易失状态。
- snapshot、membership change 和 PreVote 尚未建模，Mapper 会明确拒绝对应语义。

`raft.tla` 的动作名和参数保持与原 controlled TLC 的 `RaftActionMapper` 兼容，
启动 TLC HTTP 服务时将本目录作为模型目录，并选择 `raft.tla`/`raft.cfg`。
旧版 `ActionMapperFactory` 对模型文件名进行大小写敏感判断，因此必须通过
`-mapperparams 'name=raft;port=2023'` 显式选择 `RaftActionMapper`；只传入小写
`raft.tla` 会错误回退到默认 Mapper。
