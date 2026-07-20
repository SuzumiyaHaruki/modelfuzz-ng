# 基础 Raft 复制与提交实验（2026-07-20）

## 目标

验证一条 Plan 能连续经过 Plan Resolver、真实 etcd-raft、Trace、Raft Mapper
和原 ModelFuzz controlled TLC，并检查真实节点与 TLA+ 最终状态的 leader、term、
日志和 commit index 是否一致。

## 配置

- 3 个节点，`PreVote=false`、`CheckQuorum=false`；
- `ElectionTick=10`、`HeartbeatTick=1`；
- seed 为 42；
- `MaxLogIndex=5`、`LargestTerm=5`；
- CLI 使用 `-strict-plan`，不接受 partial、skipped 或 empty queue；
- TLC 使用 `-mapperparams 'name=raft;port=2026'`。

## 结果

| Plan | Action | Effect | 模型事件 | TLC状态 | 真实Raft最终状态 | TLC最终状态 |
|---|---:|---:|---:|---:|---|---|
| `election-commit-node1` | 6 | 16 | 9 | 9 | n1 leader；n1/n2 log=1、commit=1 | 一致 |
| `election-commit-node2` | 6 | 16 | 9 | 9 | n2 leader；n2/n3 log=1、commit=1 | 一致 |
| `client-request-commit` | 9 | 22 | 13 | 12 | n1 leader；n1/n2 log=2、commit=2 | 一致 |
| `follower-catchup-multi-entry` | 11 | 26 | 17 | 16 | n1 leader；n1/n2 log=4、commit=4 | 一致 |

客户端请求实验的最终日志为：

```text
n1: [(term=1,value=0), (term=1,value=1)]
n2: [(term=1,value=0), (term=1,value=1)]
```

其中 value 0 是 leader no-op，value 1 是客户端请求。n1、n2 的真实
`commit=applied=2`，TLC 中对应 `commitIndex=<<2,2,0>>`。

多 entry 追赶实验先让 leader 连续接收值 `1`、`2`、`3`，但只向节点 2 投递
no-op。节点 2 确认 no-op 后，真实 etcd-raft 生成一个包含三条客户端 entry 的
`MsgApp(index=1, entry_count=3)`。Mapper 依据同一步真实
`MsgAppResp(reject=false,index=4)`，把该批次依次映射为 index `1`、`2`、`3`
的三条模型事件。最终真实节点 1/2 的日志均为：

```text
[(term=1,value=0), (term=1,value=1), (term=1,value=2), (term=1,value=3)]
```

真实状态为 `commit=applied=4`，TLC 为 `commitIndex=<<4,4,0>>`，leader、term、
日志内容和 commit index 全部一致。

TLC 状态数不保证恒等于“事件数+1”，因为服务端状态抽象可能合并连续重复状态。
三个实验均返回 `status=completed`，没有发生解析降级、Runtime 错误、映射错误
或模型执行错误。

## 严格重放

使用持久化后的 `config.json` 和 `trace.json` 重新创建集群并逐字段比较：

| Plan | 匹配步骤 | 结果 |
|---|---:|---|
| `election-commit-node1` | 6/6 | completed |
| `election-commit-node2` | 6/6 | completed |
| `client-request-commit` | 9/9 | completed |
| `follower-catchup-multi-entry` | 11/11 | completed |

自动测试另外覆盖了 MessageID、Effect 和 ObservationDigest 篡改。三类篡改均在
第一处差异返回 `trace replay diverged`，且消息身份不一致时不会执行该 Action。

## 模型能力预检

使用 `unsupported-crash.json` 请求 crash 节点 1。Engine 返回
`unsupported_by_model`，Concrete Action 和 Trace Step 均为 0；最终 Observation
仍显示节点 1 为 running、epoch 1、follower，证明拒绝发生在真实 SUT 被修改前。

消息 Observation 现在携带 Adapter 的稳定 Metadata，因此 Profile 可以在投递
前识别 `MsgApp.entry_count`。Trace 格式升级到 v3；v2 轨迹仍可严格重放，但旧版
ObservationDigest 因不包含消息 Metadata 而不参与比较。实际 v2 客户端提交轨迹
已成功重放到 v3 Runtime，匹配 9/9 步。

完整 JSON 产物保存在本地 `runs/basic-raft-20260720/`；`runs/` 默认被
`.gitignore` 排除，避免把体积较大的实验输出提交到源码仓库。
