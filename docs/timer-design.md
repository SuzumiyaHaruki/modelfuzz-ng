# ModelFuzz-NG Timer 设计讨论总结

## 1. 文档目的

本文总结 ModelFuzz-NG 在 timer 建模方面遇到的困难、已经确定的设计，以及仍需在 Adapter 和 Plan 层解决的问题。

当前方案优先追求：

- 继承 etcd-raft 已有的 `Tick()` 时间抽象；
- 允许 LLM 使用强制超时快速进入更多协议状态；
- 保留自然心跳、自然选举和消息延迟；
- 避免为不同节点角色和 timer 来源创建大量 ActionKind；
- 保证实际执行可以记录、比较和严格重放；
- 第一版 Raft Adapter 尽可能简单。

本文描述的基础 Adapter、Plan 数据结构和 Resolver 已经实现；Engine、动态节点
选择器和更高级的宏仍属于后续工作。

## 2. Timer 设计的主要困难

Timer 不是一个孤立动作，它同时受到以下状态影响：

- 全局逻辑时间；
- 节点是否运行；
- 节点当前角色；
- 最近是否收到心跳或 AppendEntries；
- 协议内部的超时计数是否被重置；
- 消息投递和 timer callback 的先后顺序；
- Candidate 是否及时获得多数票；
- crash/restart 是否改变了节点 epoch。

因此，“让节点 A 超时”可能具有两种完全不同的含义：

1. 等待其自然 timer 到期；
2. 为了状态探索，立即向节点注入一次抽象超时。

这两种行为需要同时支持，但不应该被建模成大量互相独立的 Action 类型。

## 3. 当前核心决策

### 3.1 自然超时不是可选择Action

自然 timeout 由 `AdvanceTime` 驱动的 `RawNode.Tick()` 产生。它不是 LLM 或调度策略手动选择的 Action，而是执行过程中被 Adapter 捕获并写入 Trace 的 Effect。

当前 core 使用：

```text
EffectTimerFired
TimerFired
```

记录自然或强制 timer 事件。

第一版不定义 `EffectSetTimer` 和 `EffectCancelTimer`。etcd-raft 只向外暴露
`Tick()`，内部通过 elapsed 计数器重置超时，Adapter 无法在不修改 Raft 的
情况下准确观测“设置”和“取消”行为。如果以后接入显式虚拟 timer 的系统，
再根据实际需求增加这类 Effect。

### 3.2 强制超时是可选择Action

强制 timeout 使用：

```text
ActionTimeout
```

它不等待 deadline，也不推进全局逻辑时间。Raft Adapter 第一版将其解释为：

> 立即让一个正在运行的 Follower 或 Candidate 进入正常的 election timeout 处理路径。

强制动作不能直接修改 Raft 的角色、term 或内部字段，必须调用 Raft 的正常 campaign/timeout 逻辑，否则会绕过 HardState、vote、Ready/Advance 和正常消息生成。

如果目标节点已经是 Leader、已经 crash，或者不满足 Adapter 的最低前置条件，Plan 执行器应将其标记为 skipped/invalid；严格 Concrete replay 应报告轨迹分歧。

### 3.3 自然与强制通过字段区分，而不是增加ActionKind

`TimerFired.Source` 区分：

```text
natural
forced
```

`TypeHint` 可以记录：

```text
election
heartbeat
check_quorum
```

`RoleHint` 可以记录触发前角色：

```text
follower
candidate
leader
```

Follower 和 Candidate 的 election timeout 不需要变成两个 ActionKind；heartbeat/check-quorum 也不需要各自成为新的 ActionKind。

## 4. 当前建议的最小动作集合

第一版可控制 Action 保持为：

```text
Deliver
Drop
Duplicate
AdvanceTime
Timeout
Crash
Restart
Request
```

其中：

- `Timeout` 表示强制超时；
- 自然 timer 触发属于 `EffectTimerFired`；
- timer 来源、类型和节点角色属于事件字段；
- Partition、FairDeliver、RunUntilLeader 等应当是 Plan 层宏，不进入低层 ActionKind。

这种设计避免以下类型膨胀：

```text
FollowerNaturalTimeout
FollowerForcedTimeout
CandidateNaturalTimeout
CandidateForcedTimeout
LeaderHeartbeatTimeout
LeaderCheckQuorumTimeout
```

## 5. 时间语义：一单位对应一轮Tick

Core 只定义离散、单调的 `LogicalTime`，不规定它与现实秒数的关系。每个 Adapter 负责声明一单位逻辑时间的含义。

etcd-raft Adapter 第一版采用：

> 一个逻辑时间单位，等于对每个存活 Raft 节点各调用一次 `RawNode.Tick()`。

例如：

```text
AdvanceTime: 10 -> 13
```

应按三轮全局 tick 执行：

```text
t=11:
  node1.Tick()
  drain node1 Ready
  node2.Tick()
  drain node2 Ready
  node3.Tick()
  drain node3 Ready

t=12:
  node1.Tick()
  drain node1 Ready
  node2.Tick()
  drain node2 Ready
  node3.Tick()
  drain node3 Ready

t=13:
  node1.Tick()
  drain node1 Ready
  node2.Tick()
  drain node2 Ready
  node3.Tick()
  drain node3 Ready
```

实现时需要保证：

- 外层循环按逻辑时间逐单位推进；
- 内层节点按 `NodeID` 稳定排序；
- crash 节点不调用 `Tick()`；
- 每个节点 tick 后立即排空该节点的 `Ready`；
- 每轮产生的 Effect 使用该轮逻辑时间；
- 处理完当前轮 Ready 后才能进入下一轮。

第一版不支持只暂停某一个节点的本地时钟或模拟节点时钟漂移。这些能力可以以后作为专门故障模型增加，不应阻塞基础 Adapter。

## 6. AdvanceTime的语义

### 6.1 Concrete Action使用绝对目标时间

当前 core 使用：

```go
Action{
    Kind:       ActionAdvanceTime,
    TargetTime: 25,
}
```

只有 `ActionAdvanceTime` 可以改变 `StepRecord.TimeBefore/TimeAfter`。其他 Action 被视为发生在同一个逻辑时间点。

Core 会验证：

- `TargetTime` 非零；
- `TimeAfter > TimeBefore`；
- `TimeAfter == TargetTime`；
- 非 `AdvanceTime` Action 不得修改逻辑时间；
- Effect 的时间必须落在 `[TimeBefore, TimeAfter]` 中；
- 相邻 Step 的逻辑时间必须连续。

### 6.2 LLM Plan更适合使用相对tick数

离线 LLM 不一定知道执行到某一步时的当前时间。因此 Plan 层更适合生成：

```text
AdvanceTicks(delta=2)
```

执行时解析为：

```text
ActionAdvanceTime(TargetTime = currentTime + 2)
```

Concrete Trace 记录最终绝对目标时间，以便严格重放。

### 6.3 AdvanceTime内部可以产生多个事件

如果一次从 10 推进到 25，Adapter 内部会执行 15 轮 tick。期间可能：

- 多次产生 heartbeat；
- 多次向消息队列添加消息；
- 一个或多个节点自然 election timeout；
- Candidate 再次 timeout；
- Leader 因可选机制改变角色；
- 产生多个 ModelEvent，并改变最终 Observation。

`AdvanceTime` 仍然只负责推进时间；具体 timeout、消息和模型事件通过
带时间的 Effect 表达，步骤结束状态由 Observation 表达。

## 7. 跨越式推进示例

假设：

```text
Now = 10
HeartbeatTick = 2
A最早在20发生election timeout
B最早在24发生election timeout
```

执行：

```text
AdvanceTime: 10 -> 25
```

可能得到：

```text
t=12  Leader产生H12
t=14  Leader产生H14
t=16  Leader产生H16
t=18  Leader产生H18
t=20  A自然超时，成为Candidate并产生VoteRequest
t=22  根据最新Raft状态继续Tick
t=24  如果B仍满足条件，则B自然超时
t=25  AdvanceTime结束
```

A 在 20、B 在 24 超时不能被批量描述成“同时超时”。到 25 时两者可能都已经超时，但 Effect 必须保留各自发生时间和先后顺序。

每轮 tick 后必须立即更新状态并处理 Ready，后续 tick 只能基于更新后的状态继续执行。

## 8. 跨越期间的消息语义

一次 `AdvanceTime` 内部不会自动执行外部 `Deliver` Action。因此中途产生但没有投递的心跳是“延迟”，不是“丢失”。

例如：

```text
H18.CreatedAt = 18
```

如果时间 25 才投递：

```text
DeliveredAt = 25
Delay       = 7
```

如果 A 已经在 20 增加 term，时间 25 才到达的旧心跳可能被忽略或产生高 term 响应。这是需要保留的测试场景。

消息在 Observation 中保存 `EnqueuedAt`。第一版不额外保存 `DeliveredAt`；
投递时间直接由 `ActionDeliver` 所在 Step 的 `TimeBefore/TimeAfter` 推导。

## 9. 时间推进频率与性能

不要求 LLM 固定生成大量 `AdvanceTicks(1)`：

```text
AdvanceTicks(1)
AdvanceTicks(1)
AdvanceTicks(1)
```

这种方式可以提供最细粒度的消息/tick 交错，但会使 Plan 和 Trace 很长。

更合理的第一版策略是给 LLM 一组有限的相对时间选择，例如：

```text
1
HeartbeatTick
ElectionTick
```

含义大致为：

- `1`：精细控制；
- `HeartbeatTick`：允许产生一轮心跳；
- `ElectionTick`：有意探索自然选举和消息延迟。

需要配置 `MaxAdvanceTicks`，防止一次很大的时间推进在内部产生海量心跳或重复选举。

第一版可以让 `AdvanceTime` 严格执行到目标；如果实践中经常产生 Election Storm，再考虑增加 Plan 层的“最多推进到某事件”宏，而不是改变低层 `ActionAdvanceTime` 的确定语义。

## 10. Plan执行与best-effort语义

LLM 生成的 Plan 不是严格 Concrete Trace。Plan 应当在执行到每一步时，根据当前状态解析。

### 10.1 批量消息投递

Plan：

```text
Deliver(link=A->B, count=5)
```

如果当前只有 3 条消息，则展开为 3 条具体 Deliver Action：

```text
Deliver(m1)
Deliver(m2)
Deliver(m3)
```

并记录：

```text
Requested = 5
Resolved  = 3
Result    = partial
```

如果队列为空，可以记录 `empty_queue`，不需要让整条探索 Plan 失败。

### 10.2 Crash和Restart

Plan 探索模式可以采用近似幂等语义：

```text
Crash(running)    -> resolved
Crash(crashed)    -> skipped
Restart(crashed)  -> resolved
Restart(running)  -> skipped
```

Concrete replay 必须更严格：如果记录的具体动作前置状态不一致，应报告 divergence，而不是静默 skip。

### 10.3 基于位置的消息选择

消息 selector 必须执行时解析：

```text
Plan: link + position/count
Runtime: 解析当前队列中的具体MessageID
Trace: 同时记录selector和MessageID
```

自然心跳和选举消息可能改变队列长度和位置，提前绑定 MessageID 会使离线 Plan 非常脆弱。

具体 `core.Action` 不再兼容只有 Selector 或只有 MessageID 的中间状态。Deliver、Drop 和 Duplicate 必须同时包含：

```text
MessageID：确定真正操作的消息
Selector：记录解析时的Link和当前Position
```

严格重放时，Runtime 使用 MessageID 确定目标，同时验证 Selector 在当前队列中是否仍然解析到该 MessageID；不一致时报告 replay divergence。

### 10.4 建议的Plan解析结果

```text
resolved
partial
skipped
invalid
empty_queue
```

这些结果只说明 Plan 是否成功解析成 Concrete Action，不代表动作已经执行。
Engine 后续应另外记录 `applied` 或 `failed` 等执行结果；两层状态都不应混入
低层 ActionKind。

## 11. 消息语义与超时目标的冲突

原始 ModelFuzz 主要按 `(from, to)` 队列和位置控制消息，不理解具体消息语义。

如果完全不使用语义，仍然可以通过不向目标节点投递任何入站消息来制造自然超时，但这相当于强网络隔离，会同时阻塞：

- heartbeat；
- AppendEntries；
- vote；
- snapshot；
- 其他协议消息。

如果希望只延迟 heartbeat、继续投递其他消息，就必须使用 Adapter 提供的 `TypeHint`。

暂定原则仍然是：

> Core/Executor 不依赖消息语义；Adapter 可以提供语义提示；策略或 LLM 使用提示选择；Concrete Action 使用 MessageID 和位置执行与重放。

## 12. 自然超时导致Plan偏离

自然 timeout 被记录并不意味着它不会影响 LLM 原计划。例如：

```text
计划：
Timeout(A)
AdvanceTicks(10)
DeliverVotesTo(A)

实际：
A被强制变成Candidate
AdvanceTicks期间A再次自然超时
A增加term
原VoteResp成为旧term消息
```

主要风险是：

- 一次时间推进插入多个自然选举；
- Candidate 无法获得多数票而反复超时；
- 后续 Plan 假设的 Leader/Candidate 身份失效；
- 消息位置被中途产生的 heartbeat/vote 改变；
- 实际轨迹与 LLM 设计意图偏离。

第一版优先采用以下简单限制：

- 限制单次 `AdvanceTicks` 最大值；
- 为整条执行设置最大 Action、tick、消息和 term 预算；
- 记录连续自然 election timeout 次数；
- 检测长期无消息投递、无 Leader、无 commit 的 Election Storm；
- Election Storm 作为执行状态/统计信息，不直接报告为协议 bug。

Follower 自然超时后也会成为 Candidate，因此 Follower 与 Candidate 并不存在本质不同的循环问题；真正要检测的是连续超时期间是否存在协议或网络进展。

## 13. 公平投递与状态广度

始终均匀地投递所有方向消息可以减少重复选举，但会损失：

- 长时间网络分区；
- 单向链路阻塞；
- 多 Candidate；
- stale message；
- term 快速变化；
- 分区恢复状态。

因此不建议强制全局均匀投递。

后续 Plan 层可以采用：

- 未显式阻塞链路的消息年龄加权；
- 有意长期阻塞使用显式 `Partition`/`BlockLink`；
- `HealPartition` 后使用 `FairDeliver`/`DrainNetwork`；
- LLM 生成“扰动 -> 收敛 -> 再扰动”的高层计划。

这些是策略能力，不应增加低层 ActionKind。

## 14. 是否需要Hybrid/NaturalOnly模式

Core 不需要定义多个 TimerMode。自然 timeout 只要执行 `AdvanceTime` 就始终存在。

是否允许强制 timeout 使用一个执行配置即可：

```go
AllowForcedTimeout bool
```

当它为 `false`：

- LLM allowed action set 不包含 `Timeout`；
- Engine 也拒绝强制超时；
- 自然 timeout 仍由 Tick 产生。

当它为 `true`：

- LLM 可以选择强制 timeout；
- 自然 timeout 仍然存在。

“Hybrid”和“NaturalOnly”可以作为实验报告中的配置名称，但不需要成为 core 类型。

发现 violation 后，可以关闭 `AllowForcedTimeout`，尝试把强制轨迹具体化为自然时间推进和消息延迟，从而判断问题是否在严格时间语义下可达。

## 15. 第一版Raft范围

第一版底层 Raft 应尽量简单，通过配置关闭暂时不需要的扩展，而不是从上游源码删除功能。

建议：

```text
PreVote            = false
CheckQuorum        = false
AsyncStorageWrites = false
MemoryStorage
3个普通voter
固定HeartbeatTick
固定ElectionTick
受控的每节点随机源
```

暂时不主动测试：

- PreVote；
- learner；
- 动态成员变更；
- leadership transfer；
- snapshot；
- ReadIndex/lease read；
- async storage；
- check-quorum；
- 真实网络和磁盘 goroutine。

关闭 PreVote 后，Follower/Candidate 的自然 election timeout 和 term 变化更直接，也更容易与 TLA+ 模型对齐。

不能省略的是随机性控制。当前本地 raft v3.7 已增加实例级 `Config.Rand`；
Adapter 根据 `ExecutionSeed + NodeID + NodeEpoch` 为每个节点创建独立的稳定随机流。
相同执行重放时会得到相同的 election timeout，节点执行顺序也不会交叉消耗随机数。

## 16. Trace与重放

建议区分：

```text
PlanSequence
    LLM生成，允许batch、selector和best-effort

ActionSequence
    Plan运行时逐步解析并实际执行的具体Action，可用于严格重放

Trace
    具体Action、带时间Effects、状态摘要和执行结果
```

运行一个 LLM 生成的 Plan 时仍然会产生 `ActionSequence`，但该序列通常不会在执行开始前完整存在。Plan Executor 每处理一个 PlanStep，就根据当时状态将它解析为零到多个具体 Action，并把真正执行的 Action 依次追加到 `ActionSequence`：

```text
PlanSequence:
  Deliver(A->B, count=5)
  AdvanceTicks(2)
  Timeout(AnyCandidate)

执行时当前A->B只有两条消息，最终得到：

ActionSequence:
  Deliver(message=m7)
  Deliver(message=m8)
  AdvanceTime(target_time=12)
  Timeout(node=2)
```

如果某个 PlanStep 因队列为空或状态不满足而被 skipped，它会出现在 Plan 执行结果中，但不会伪造一条没有真正执行的 Action。自然 timer 触发同样不进入 `ActionSequence`，而是作为对应 `AdvanceTime` Step 的 `EffectTimerFired` 记录。

因此“Concrete ActionSequence”的含义不是“必须在运行前由 LLM 完整写好”，而是：

> 其中每一条都是已经解析到具体 NodeID、MessageID 和 TargetTime，并且真实执行过的低层 Action。

当前 `core.ActionSequence` 不直接承担尚未解析的 LLM Plan；执行结束后，可以绕过 Plan 的 batch、selector、fallback 等动态解析过程，直接使用它进行严格重放。

严格重放自然 timeout 时，不需要从 Trace 中再次选择 `FireTimer`；重放相同 `AdvanceTime`，使用相同 Raft 配置和随机 seed，然后比较产生的 `EffectTimerFired`、消息、ModelEvent 和 Observation 摘要。

如果自然事件出现时间或内容不同，应报告 replay divergence。

## 17. 当前core代码映射

本轮设计已经反映到以下 core 数据模型中：

- `ActionFireTimer` 已从可执行 ActionKind 中移除；
- 新增 `ActionTimeout`，表示强制 timeout；
- `Action.Time` 重命名为 `Action.TargetTime`；
- 新增 `EffectTimerFired`；
- 新增 `TimerFired`，包含 node、epoch、source、TypeHint 和 RoleHint；
- 每个 `Effect` 新增 `At LogicalTime`；
- `StepRecord` 限制只有 `AdvanceTime` 可以修改时间；
- `StepRecord` 校验 Effect 时间必须位于该步骤时间区间；
- Trace 相邻步骤时间必须连续；
- `ActionSequence` 注释明确其为可严格重放的 Concrete ActionSequence。
- Deliver、Drop 和 Duplicate 具体 Action 必须同时携带 MessageID 与 Selector。

## 18. 尚未解决的问题

以下问题仍需在后续 Engine、策略和扩展 Adapter 中决定：

1. 当前通过状态变化和 Ready 消息推断 timer，何时需要增加更精确的 Raft hook；
2. 总 tick budget 和 Election Storm 阈值应放在 Engine 的哪一层；
3. 周期性 heartbeat 大量堆积时是否允许降权、合并或压缩；
4. 是否需要在 Message 中持久化 `DeliveredAt`，还是从 Trace 推导；
5. 强制 timeout 轨迹如何自动具体化为消息阻塞和自然时间推进；
6. 如何衡量 Plan 与实际 Concrete Trace 的偏离程度；
7. 何时增加动态节点选择器和 Partition、FairDeliver 等 Plan 宏；
8. 何时引入 PreVote、CheckQuorum、snapshot 等扩展进行第二阶段测试。

## 19. 当前推荐方案摘要

1. Core 逻辑时间与墙上时间无关；etcd-raft 中一单位对应所有存活节点的一轮 `Tick()`；
2. `AdvanceTime` 是唯一改变全局逻辑时间的 Action；
3. 自然 timeout 不是可选择 Action，而是带时间的 `EffectTimerFired`；
4. 强制 timeout 使用单一 `ActionTimeout`，不等待 deadline、不推进时间；
5. timer 来源、类型和角色使用字段记录，不增加 ActionKind；
6. LLM Plan 使用相对 `AdvanceTicks` 和 best-effort batch/selector；
7. Concrete Action 使用绝对 `TargetTime`、MessageID 和确定 NodeID；
8. 一次较大的 `AdvanceTime` 可以产生多次心跳、多个自然超时和多条消息，所有 Effect 必须保留发生时间；
9. 自然与强制 timeout 始终可以共存，是否允许强制行为只由 `AllowForcedTimeout` 控制；
10. 第一版关闭 PreVote、CheckQuorum 和其他可选扩展，使用简单内存内 Raft；
11. 使用受控随机源、稳定节点顺序和严格 Trace 校验保证重放；
12. 通过 tick/Action budget、Election Storm 检测和后续 Plan 策略控制自然事件造成的偏离。
