# ModelFuzz 系统问题与不足梳理

本文整理当前 ModelFuzz 系统在**模型表达能力**、**调度语义**、**trace 可复现性**和**TLA+ 反馈使用方式**上的主要问题。这里暂不讨论工程健壮性、异常处理、配置校验等实现细节。

## 0. 主要组件总览

| 组件                  | 一句话说明                                                             |
| --------------------- | ---------------------------------------------------------------------- |
| Fuzzer                | 主循环，负责生成/重放调度、驱动系统执行，并根据反馈继续探索。          |
| Cluster / Environment | 被测系统适配层，负责启动节点、停止节点、投递消息、注入请求和推进时间。 |
| Guider                | 根据 TLC 状态覆盖、trace 覆盖或代码覆盖判断一次执行是否值得继续变异。  |
| Mutator               | 对已有调度轨迹做局部修改，生成新的候选执行。                           |
| TLCClient             | 把执行过程中记录的模型事件发送给 TLC server，并取回模型状态路径。      |
| Measure               | 离线重放已保存的事件轨迹，重新计算 TLC 覆盖率。                        |

---

## 1. 网络模型仍然比较简化

当前 ModelFuzz 的网络调度以 `from -> to` 方向队列为单位。每条方向队列是 FIFO 队列，一次 schedule 的语义大致是：

```text
从 from -> to 队列中取出前 maxMessages 条消息并投递
```

因此，当前网络模型更接近：

```text
per-link FIFO + inter-link reorder + implicit delay
```

也就是说：

- **per-link FIFO**：同一个 `from -> to` 队列内部保持 FIFO 顺序。
- **inter-link reorder**：不同 `from -> to` 队列之间可以通过调度顺序体现乱序。
- **implicit delay**：如果某条队列暂时没有被选中，其中的消息就会留在队列里，相当于被延迟。

这里的 delay 并不是一种显式动作。trace 中没有类似 `Delay(message, k)` 的记录，只是通过“不投递某条队列”间接产生延迟效果。

这种模型比较容易实现，也适合描述一些基于连接的 RPC 场景。但它无法完整表达更一般的异步网络语义，例如：

- 同一 `from -> to` 队列内部的消息乱序。
- 对某一条具体消息执行 drop。
- 对某一条具体消息执行 duplicate。
- 明确记录某条消息被延迟了多久或延迟了多少个调度点。

所以，当前网络模型的表达能力是有限的。

---

## 2. 同一方向队列内无法重排消息

当前系统只能在不同方向队列之间制造乱序。例如：

```text
先投递 1 -> 2 的消息
再投递 3 -> 2 的消息
```

或者：

```text
先投递 2 -> 1 的消息
再投递 1 -> 3 的消息
```

但是对于同一个方向队列，例如 `1 -> 2`，如果队列中已有：

```text
m1, m2, m3
```

当前 schedule 只能按 FIFO 投递：

```text
m1
m1, m2
m1, m2, m3
```

它不能直接表达：

```text
m2
m3
m2, m1
```

这说明当前所谓的“消息乱序”主要发生在不同 link 之间，而不是同一个 link 内部。

如果被测系统或 TLA+ 模型假设网络是完全异步、非 FIFO 的，那么当前调度模型会比模型语义更弱。它可能无法覆盖一些依赖 same-link reorder 的执行路径。

---

## 3. 没有显式的消息丢弃语义

当前系统没有独立的 `DropMessage` 或类似动作。

消息进入 `from -> to` 队列后，要么在某次 schedule 中被 FIFO 取出并投递，要么一直留在队列里直到 iteration 结束。

因此，系统只能表达一种弱形式的“丢弃”：

```text
后续再也不选择这条队列，于是其中的消息永远不被投递
```

但这不是显式 drop，因为 trace 中并没有记录：

```text
某一条消息被丢弃
```

这会带来几个问题：

- 无法区分“消息仍在网络中延迟”和“消息已经丢失”。
- 无法对单条消息建模 drop，只能让整个方向队列长期不被调度。
- event trace 中不会出现丢包事件，TLA+ 模型也无法直接观察这类网络行为。

对需要测试丢包、重传、超时等行为的协议来说，这一点会限制状态空间探索。

---

## 4. trace 记录的是调度形状，不是具体消息身份

当前 trace 中的 Node Choice 主要记录：

```text
from
to
maxMessages
```

它表示从某个方向队列中取前若干条消息，但没有记录具体是哪几条消息。

这意味着：如果两次实验中，同一个 `from -> to` 队列内部消息顺序不同，那么完全相同的 schedule 也可能投递到不同消息。

例如，某条 trace 中记录：

```text
Schedule(1, 2, 1)
```

第一次运行时，`1 -> 2` 队头是 `m1`，于是投递 `m1`。

第二次运行时，如果由于系统内部非确定性，`1 -> 2` 队头变成了 `m2`，那么同一条 trace 实际投递的就是 `m2`。

所以当前 trace 更像是：

```text
调度策略记录
```

而不是：

```text
消息级 replay 脚本
```

这会影响 bug 复现的严格性。系统隐含要求 `Cluster.Tick()` 对同一方向队列产生消息的顺序必须稳定，否则 replay 语义会变弱。

---

## 5. step 模型固定，动作表达粒度较粗

当前一次 iteration 被划分为固定数量的 `Steps`。每个 step 的执行顺序基本固定：

```text
可选 Stop
可选 Start
一次消息 schedule
可选 ClientRequest
一次 Tick
```

这种模型简单直观，但表达能力比较固定。

主要限制包括：

- 一个 step 最多只能包含一次 crash。
- 一个 step 最多只能包含一次 restart。
- 一个 step 最多只能包含一次 client request。
- 每个 step 都会进行一次消息 schedule 尝试。
- schedule 的位置固定在 crash/start 之后、client request 之前。
- Tick 固定发生在每个 step 的最后。

因此，当前系统不太容易表达更自由的 action sequence。例如：

```text
ClientRequest
Tick
Crash
DeliverMessage
Restart
DropMessage
Tick
```

在当前实现中，trace 不是由一串任意原子动作组成，而是由固定 step 模板展开得到。

这会让某些调度 interleaving 难以表达，尤其是当我们想精确控制 crash、request、deliver、tick 之间的相对顺序时。

---

## 6. crash/restart 与超时控制的是离散步骤，不是真实时间

系统中没有真实时间或 wall-clock time 的概念。所谓 crash/restart 的“时间”，本质上是它们出现在第几个 step。

例如：

```text
step 3: Stop node 2
step 7: Start node 2
```

这并不表示节点 2 宕机了某个具体时间长度，而是表示在 step 3 到 step 7 之间的调度动作中，节点 2 被视为 crashed。

因此，系统实际控制的是：

```text
故障动作在 trace 中的顺序位置
```

而不是：

```text
真实时间持续多久
```

这一点本身没有问题，因为很多模型检查和系统测试都使用离散步骤。但如果 TLA+ 模型或真实系统依赖超时、心跳间隔、租约时间等概念，当前 step 模型和真实时间语义之间就需要非常谨慎地对齐。

以 etcd-raft 为例，它的核心协议代码并不直接读取真实时钟，也不直接判断“已经过去了多少毫秒”。Raft 包对外暴露的是 `Tick()` 接口，协议内部通过 `electionElapsed`、`heartbeatElapsed` 这类计数器记录逻辑时间推进。`ElectionTick` 和 `HeartbeatTick` 表示的都是 `Tick()` 调用次数：外层系统每调用一次 `Tick()`，Raft 内部的计数器前进一步；当 `electionElapsed` 达到随机化后的 `randomizedElectionTimeout` 时，节点才会触发 election timeout。

不过，在真实部署中，一次 `Tick()` 的触发通常仍然来自物理时间。etcd-raft 的官方使用方式一般是外层程序启动一个周期性定时器，例如 `time.Ticker`，然后每隔固定时间调用一次 `Node.Tick()`：

```text
每 100ms 触发一次 time.Ticker
  -> 调用一次 Node.Tick()
  -> Raft 内部逻辑时间前进一步
```

因此，生产环境中的超时可以理解为：

```text
真实时间定时器
  -> 周期性触发 Tick()
  -> 多次 Tick() 累积成 Raft 内部超时
```

也就是说，`Tick()` 并不是完全脱离物理时间，而是把物理时间和协议逻辑之间隔了一层抽象。真实系统可以用物理定时器驱动它，测试系统也可以绕过真实等待，直接手动调用它。

这种设计对 fuzzing 是有利的，因为测试框架获得了一个明确的手动介入口。ModelFuzz 不需要真的等待几百毫秒或几秒，而是可以在每个 step 末尾主动调用 `Cluster.Tick()`，具体到 raft-fuzzing 中还可以通过 `TicksPerStep` 控制一个 step 内对每个节点调用多少次 `node.Tick()`。例如，连续让某个 follower 执行足够多次 `Tick()`，并且中间不投递 leader 的 heartbeat，就更容易稳定构造出选举超时场景。

因此，在 raft-fuzzing 中，step 内 Stop、Start、消息投递和 ClientRequest 的实际执行耗时不会自动折算成 Raft 逻辑时间，只有 step 末尾的 `Tick()` 调用才会推进超时计数。

但这也意味着，ModelFuzz 控制的是离散逻辑时间推进方式，而不是物理时间本身。对于 etcd-raft 这类已经采用 `Tick()` 抽象的系统，这种模型比较自然；对于真实实现中强依赖 wall-clock time 的协议或机制，则需要注意两者语义是否一致。

另外，当前随机路径中 crash 和 restart 可能发生在同一个 step。主循环顺序是先 Stop 再 Start，因此可能出现：

```text
同一步内先宕机，再恢复
```

这种故障语义比较短暂，是否有意义取决于具体协议和模型假设。

---

## 7. mimicTrace 不是严格完整的 replay 脚本

`mimicTrace` 是某次 iteration 的输入目标轨迹，而 `trace` 是本轮边执行边生成的实际输出轨迹。

当前 `config.Steps` 是每次 iteration 的统一执行长度上限，但每条 `mimicTrace` 的长度不一定等于 `Steps`。

具体行为如下：

| mimicTrace 与 Steps 的关系 | 当前行为                                   |
| -------------------------- | ------------------------------------------ |
| mimicTrace 比`Steps` 短  | 先按 mimicTrace 执行，后续 step 随机补齐   |
| mimicTrace 等于`Steps`   | 基本按目标轨迹执行                         |
| mimicTrace 比`Steps` 长  | 只执行前`Steps` 范围内能消费或触发的部分 |

因此，`mimicTrace` 更像：

```text
引导前缀 / 参考轨迹
```

而不是：

```text
严格 replay script
```

这会带来一个重要问题：同一条 mimicTrace 在重放时，如果它比配置的 Steps 短，后半段会进入随机状态。这样得到的执行结果并不完全由 mimicTrace 决定。

对于探索来说，这种设计可以让系统从已有轨迹继续向后随机扩展；但对于复现来说，它会削弱 trace 的确定性。

---

## 8. seed 阶段和正式反馈循环之间存在重复执行

`seed()` 的作用是随机执行一批 trace，并把它们放入 `mutatedTracesQueue`，作为后续搜索的初始 population。

当前流程大致是：

```text
seed 阶段：
  随机执行 trace A
  保存 A 的 choice trace
  放入 mutatedTracesQueue

正式 Run 阶段：
  从 mutatedTracesQueue 取出 trace A
  作为 mimicTrace 再执行一次
  将新的 eventTrace 交给 Guider.Check
```

也就是说，seed trace 会先被执行一次，然后在正式反馈循环里再次执行。

这说明 seed 阶段第一次执行主要用于生成一条可用的 choice trace，而不是直接参与 Guider 的覆盖反馈。

这种设计带来的不足是：

- seed 阶段已经执行过的 trace 后续还要再执行一次。
- seed 阶段第一次执行产生的 event trace 没有直接用于状态覆盖统计。
- 如果 Cluster 存在非确定性，seed 第一次执行和后续 mimic 重放可能产生不同 event trace。

---

## 9. 自动事件类型较少，协议语义主要依赖 AddEvent

框架自动记录的通用事件主要有四类：

```text
SendMessage
DeliverMessage
Add
Remove
```

它们分别描述：

- 节点发送消息
- 消息被投递
- 节点加入或恢复
- 节点移除或宕机

这些事件足以描述外部网络和节点可用性变化，但不足以描述具体协议语义。

例如在 Raft 中，TLA+ 模型可能还需要看到：

- leader 产生
- candidate timeout
- client request
- log append
- commit index 推进
- 状态机 apply
- vote granted 或 vote rejected

这些事件都需要具体 Cluster 通过 `FuzzContext.AddEvent` 手动补充。

这种设计的问题在于：框架只提供了一个自由的事件出口，但没有统一约束事件 schema。

当前事件结构大致是：

```go
Event{
    Name: string,
    Params: map[string]interface{},
}
```

它很灵活，但也比较脆弱：

- 事件名是否和 TLA+ action 对应，框架无法检查。
- 参数是否齐全，框架无法检查。
- 参数类型是否正确，框架无法检查。
- 关键协议事件是否漏记，框架无法检查。

因此，event trace 的语义质量高度依赖具体 Cluster 实现者。

---

## 10. Guider 的反馈信号比较粗

当前 `TLCStateGuider` 的核心反馈是：

```text
这次 eventTrace 产生了多少新的模型状态
```

也就是 state coverage。

这种反馈比纯随机或代码覆盖更接近协议语义，是系统的一个重要创新点。但目前反馈仍然比较粗：

- 只统计唯一状态数量，不区分状态的重要性。
- 只知道本轮有没有新状态，不知道新状态主要由哪个 event 或哪个 choice 触发。
- `stateTracesMap` 记录了状态路径 hash，但没有进一步参与搜索。
- `tracesMap` 记录了 choice trace hash，但没有进一步参与搜索。
- Guider 与 Mutator 之间的连接比较简单，只是“有新状态就多生成一些 mutation”。

也就是说，TLA+ 模型已经提供了很强的语义信息，但当前系统利用得还比较浅。

---

## 11. Mutator 仍然偏简单

当前 mutator 主要是对已有 choice trace 做局部交换。

主要包括：

- 交换 Node Choice 的位置
- 交换 Node Choice 的 `MaxMessages`
- 尝试交换 crash 节点

这些变异能改变部分调度顺序和消息投递批量，但整体表达能力有限。

主要不足包括：

- 缺少对 crash/restart 顺序位置的系统性变异。
- 缺少对 client request 位置和数量的变异。
- 缺少对 message drop、message duplicate、same-link reorder 的变异。
- 缺少基于 event trace 中关键协议事件的定向变异。
- 缺少基于 TLC state trace 差异的定向变异。

因此，当前 Mutator 更像通用 trace shuffle，而不是充分利用模型反馈的 semantic mutator。

另外，现有实现中还有一个语义不一致点：`SwapCrashNodeMutator` 匹配的是 `"Crash"`，而 Fuzzer 记录宕机选择使用的是 `"StopNode"`。这说明当前变异器和 trace 类型设计之间还没有完全统一。

---

## 12. choice trace、event trace 和 state trace 的对应关系不够直接

系统中实际存在三类轨迹：

| 轨迹         | 含义                                      | 主要用途          |
| ------------ | ----------------------------------------- | ----------------- |
| choice trace | Fuzzer 做出的调度选择                     | replay、mutation  |
| event trace  | 真实系统产生的模型事件                    | 发送给 TLC        |
| state trace  | TLC 执行 event trace 后得到的模型状态序列 | coverage feedback |

这三类轨迹是 ModelFuzz 的核心，但它们之间的映射关系并不完全直接。

例如：

- 一个 Node Choice 可能产生 0 条、1 条或多条 `DeliverMessage`。
- 一次 Tick 可能产生 0 条、1 条或多条 `SendMessage`。
- 一个 AddEvent 事件可能对应协议内部的复杂状态变化。
- 一个新的 TLC state 很难直接追溯到某个具体 schedule choice。

这会增加理解和分析难度。尤其当某条 trace 发现新状态或触发异常时，当前系统并不能很直接地说明：

```text
是哪一个调度选择导致了哪个事件，
又是哪一个事件推动模型进入了哪个新状态。
```

---

## 13. 总体判断

ModelFuzz 的核心思路是有创新性的：它用 TLA+ 模型状态覆盖来引导分布式系统 fuzzing，而不是只依赖随机调度或代码覆盖。

但是从当前系统设计看，它还更接近一个原型框架。主要不足集中在：

- 网络语义较弱，主要是 per-link FIFO。
- trace 不是严格的 message-level replay。
- step 模型固定，action 表达粒度较粗。
- 事件建模依赖具体 Cluster 手动补充。
- Guider 使用了模型覆盖，但反馈信息利用得还比较浅。
- Mutator 还没有充分结合协议语义和模型状态路径。

因此，当前系统已经展示了“用模型指导 fuzzing”的价值，但在调度表达能力、复现语义和模型反馈利用深度上仍然有明显提升空间。
