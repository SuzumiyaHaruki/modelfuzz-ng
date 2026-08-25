# ModelFuzz 引入 LLM 的技术改进讨论

ModelFuzz 当前 seed 生成、trace 变异和策略调整仍然偏随机。引入 LLM 的目标是在输入生成和搜索策略层面加入协议语义。

所有 LLM 输出都应经过确定性代码检查，确认节点、step、`MaxMessages`、crash/restart 等字段合法后，再转换为 ModelFuzz 能执行的调度选择。

## 1. Trace Summarizer

完整 trace 和 event trace 通常很长，不适合直接交给 LLM。可以先用确定性代码生成摘要，例如总 step 数、client request 位置、crash/restart 位置、timeout/leader change/commit 出现情况、各 `from -> to` 队列投递和积压情况等。这个摘要可以作为后续 LLM mutator、seed generator 和覆盖停滞分析的共同输入。

## 2. LLM-guided Mutator

现有 Mutator 主要随机交换调度点、改变 `MaxMessages` 或替换 crash 节点。LLM 可以根据 trace 摘要和覆盖反馈提出更有语义的变异建议，例如延迟 leader 消息、把 client request 移到 election 附近、在 quorum 形成前 crash 节点、restart 后投递旧消息等。LLM 不直接生成最终 trace，而是生成结构化 mutation hint，再由代码校验并改写 SchedulingChoice trace。

## 3. LLM-generated Seed

随机 seed 有时很难快速进入协议关键状态。LLM 可以生成少量结构化初始场景，例如正常提交路径、leader crash before commit、follower restart 后收到旧消息、少数派先收到 response、多 client request 交错等。

## 4. Coverage Stagnation Analyzer

当 Guider 连续多轮没有发现新的 TLC state 时，可以调用 LLM 分析最近若干轮 trace 摘要，判断搜索可能卡在哪里。例如请求太少、消息投递太集中、没有触发 timeout、leader 太稳定、crash 发生太早等。

## 5. Network Scheduling Advisor

ModelFuzz 的调度核心之一是控制 `from -> to` 队列的消息投递。LLM 可以专门给出网络调度建议，例如延迟 leader 到 follower 的 heartbeat、降低某些方向的 `MaxMessages`、优先投递旧 term 消息、让某些队列长期积压等。

## 6. Crash/Restart Strategy Advisor

共识协议中的故障时机非常关键。LLM 可以根据 event trace 摘要建议更敏感的 crash/restart 位置，例如 leader 发出日志复制后 crash、candidate 选举过程中 crash、节点 restart 后先接收旧消息等。

## 7. Request Workload Generator

如果被测系统支持更复杂的请求类型，LLM 可以生成更有意义的 workload，例如读写交错、重复请求、同一 key 连续写、事务冲突、配置变更等。对当前 etcd-raft 版来说，客户端请求主要是整数编号，因此这个方向作用有限；对 RedisRaft 这类真实系统测试会更有价值。

## 8. Bug Hypothesis Generator

LLM 可以基于协议知识提出潜在 bug 假设，例如 leader 在少数派中错误提交、旧 term 消息影响新 leader、restart 后重复 apply、snapshot 和 append 乱序等。这些假设不能直接当作 bug 结论，但可以转化为 seed 或 mutation 目标，用来引导搜索进入更可疑的状态空间。
