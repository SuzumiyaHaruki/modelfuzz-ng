# ModelFuzz 状态迁移归因与定向变异实验计划

## 1. 研究目标

本工作的核心问题不是“给 ModelFuzz 加入 AI”，而是：

> ModelFuzz 已经知道一条执行是否覆盖了新的 TLA+ 状态，能否进一步把新状态回溯到真实执行事件和调度 choice，并利用该位置与模型语义减少无效随机变异？

研究主线固定为：

```text
模型状态覆盖
  → 新状态迁移溯源
  → 局部定向变异
  → 模型语义变异
  → 可选的自适应/AI 候选排序
```

AI 不是前置条件。若局部定向变异已经获得主要收益，或 AI 无法超过确定性策略，则最终工作不强行包含 AI。

## 2. 当前工作范围

### 2.1 允许修改

当前 Go 源码、测试和模型修改只发生在：

```text
modelfuzz_src/modelfuzz_artifact/raft-fuzzing
```

当前测试对象仅为该目录中的内存版 etcd-raft fuzzer。为了提供逐事件 transition
provenance，服务端的最小协议修改位于：

```text
modelfuzz_src/modelfuzz_artifact/tlc-controlled-with-benchmarks/tlc-controlled
```

主实验直接使用 ModelFuzz artifact 的已有模型：

```text
../tlc-controlled-with-benchmarks/tla-benchmarks/Raft/model/raft_alt.tla
../tlc-controlled-with-benchmarks/tla-benchmarks/Raft/model/RAFT_5_3.tla
../tlc-controlled-with-benchmarks/tla-benchmarks/Raft/model/RAFT_5_3.cfg
```

`raft_enhanced` 及既有 Enhanced pilot 仅用于模型敏感性诊断，不进入主实验对比。

### 2.2 暂不修改

- 其他 ModelFuzz 测试对象和 TLC 服务端；
- TLC 的状态抽象、动作执行和既有 `states/keys` 覆盖语义；
- RedisRaft、2PC、Coyote 和 ConsensusSeam；
- 当前固定 Step 模板；
- `from -> to` FIFO 队列和批量 `MaxMessages` 控制语义；
- 同链路重排、精确消息选择、显式 drop/duplicate、真实时间控制；
- TLA+ 协议动作本身。

如果后续证据表明上述限制直接阻断当前研究问题，必须先在本文档记录具体失败现象、拟修改范围和对应研究问题，再实施修改。

## 3. 术语与归因边界

### 3.1 三层轨迹

```text
SchedulingChoice trace
  → implementation Event trace
  → TLA+ model state trace
```

- `SchedulingChoice`：Fuzzer 的控制输入，如 Node、StopNode、StartNode、ClientRequest。
- `Event`：真实 Raft 执行后观察到的事件，如 DeliverMessage、Timeout、BecomeLeader。
- `State`：TLC 返回的模型状态及 fingerprint key。

### 3.2 Transition provenance

当前“归因”首先只表示迁移来源：

```text
新状态首次出现在哪个 event prefix 之后
该 event 来自哪个 step、phase、choice 和批内消息序号
```

它不自动证明该 choice 是新状态的唯一原因。直接触发事件可能依赖很早之前的调度前缀。

### 3.3 Counterfactual responsibility

只有在修改或删除某个 choice 后重放，并观察目标状态是否消失，才称为反事实责任验证。该实验属于后续阶段，不与基础迁移溯源混为一谈。

## 4. 研究问题

### RQ1：归因可行性

逐输入事件 transition provenance 能否把新模型状态稳定定位到实现 event 和 SchedulingChoice，
并对旧服务端保留前缀探测兼容路径？

### RQ2：归因成本

transition provenance 增加多少响应和处理开销？旧服务端回退时，前缀探测增加多少
TLC 请求、延迟和总运行时间？

### RQ3：局部变异效果

仅提高新状态附近 choice 的变异概率，能否相对原始全局随机变异提高单位执行次数和单位时间的新状态发现率？

### RQ4：模型语义的额外价值

在位置相同的情况下，使用模型 action、状态内容和事件类型选择 mutation operator，是否优于不理解协议语义的局部随机变异？

### RQ5：自适应或 AI 的额外价值

在合法候选集合固定后，自适应 operator 策略或 AI 排序是否还能稳定超过确定性语义启发式，并覆盖其额外运行成本？

## 5. 核心假设

- H1：大多数新增模型状态可以定位到带有效 `EventOrigin` 的单个 event 边界。
- H2：同一调度 trace 重放时，新增状态 key 和首次 event index 大体稳定。
- H3：保持到达新状态的关键前缀、优先变异其附近或后缀，比整条 trace 全局随机交换更有效。
- H4：模型语义可以帮助选择 crash、restart、批量大小和网络调度类算子。
- H5：AI 只有在超过局部随机、手写语义策略和轻量自适应策略时才构成有效增量。

## 6. 已完成的实现基线

截至当前版本，工作副本已经完成：

- `EventOrigin`：记录 Step、Phase、ChoiceIndex、DeliveryOrdinal、DeliveryCount；
- crash、restart、deliver、client request、tick 的来源传播；
- `TLCClient.SendTrace` 无副作用发送，不再污染输入 event trace；
- TLC HTTP 状态码和 States/Keys 长度检查；
- 服务端逐输入事件 transition provenance，以及旧服务端的 event-prefix 回退定位；
- `Guidance.NewStates` 和 `LastGuidance()`；
- trace JSON 中的 `event_origins` 和 `new_state_attributions`；
- `--state-attribution` 可选开关；
- ClientRequest Step 重放修复；
- 只有实际执行的 crash/restart 才产生 Remove/Add 模型事件；
- 单元测试、race test 和 `go vet`；
- 真实 TLC + 原始 `RAFT_5_3/raft_alt` 的 leader/client-request live smoke；
- `phase-a` 单策略入口和独立结果目录；
- Fuzzer、Mutator 和 Raft 选举随机源共享固定 seed；
- attribution 请求数、cache hit、定位结果和耗时累计统计。

Live smoke 已验证：

```text
Timeout(1)       → 原模型动作但被 abstract state 折叠
BecomeLeader(1)  → leader 新状态
ClientRequest(1) → log 新状态
```

## 7. 分阶段实验

## Phase A：真实 Fuzzer 端到端归因

### 目标

在不改变 mutation policy 的情况下，用真实 Fuzzer trace 验证来源记录、批量投递归因、重放稳定性和前缀探测成本。

### 实施内容

1. 增加只运行 `tlcstate` 的单策略入口，避免现有 compare 的四个 guider 共用 `traces` 目录。
2. 增加统一可配置随机种子，Fuzzer 和所有 Mutator 不再各自使用 `time.Now()`。
3. 在 Guidance 或运行统计中记录：
   - 完整 event 数；
   - 本轮新增状态数；
   - located、initial、failed、missing-origin 数；
   - TLC 总请求数和前缀 cache 命中数；
   - 完整 Check 与归因分别耗时；
   - 每个 choice 对应的 event 数和新状态数；
   - 批量消息的 actual delivery count 与 novelty ordinal。
4. 保存可重放 trace，并对同一 trace 至少重复执行三次。

### Pilot 配置

```text
Model: RAFT_5_3 / raft_alt / abstract
Replicas: 3
Requests: 1
Runs: 5 independent seeds
Episodes: 100
Horizon: 100
Attribution: enabled
Mutation policy: current ModelFuzz policy
```

### 输出

- attribution summary；
- TLC 请求与时间开销；
- 批量投递归因样例；
- replay stability 数据；
- 失败归因的逐项原因。

## Phase B：局部随机变异

### 目标

仅检验“知道在哪里变异”是否有效，不加入 Raft 语义或 AI。

### 候选位置策略

- `M1/global`：原始三个 Mutator 在全轨迹选择候选；
- `M2/localized`：保持原交换次数，从所有新状态 Origin 最近的20个 Node choices
  和4个 StopNode choices中选择；只有初始状态、没有可定位 Origin 时回退全局候选。

第一版不同时搜索窗口参数。只有 M2 显示稳定收益后，再比较：

```text
d ∈ {1, 3, 5, 10}
```

### 批量投递局部细化

第一版 M2 不使用 DeliveryOrdinal 改写 `MaxMessages`，只限制原始 SwapMaxMessagesMutator
的候选位置。以下规则留作后续消融：

若新状态由批内第 `r` 条消息首次触发，生成：

```text
MaxMessages = r - 1
MaxMessages = r
MaxMessages = r + 1
```

合法范围外的候选直接省略。该策略不改变固定 Step 和 FIFO 语义。

## Phase C：模型语义变异

### 目标

检验状态内容和模型 action 是否比单纯位置反馈提供额外价值。

首批确定性规则仅覆盖已有调度能力：

- Timeout/BecomeLeader：调整附近心跳链路、candidate/leader 相关调度和 crash 窗口；
- AppendEntries request/response：调整对应 from/to choice、位置及 MaxMessages；
- AdvanceCommitIndex：调整 quorum 回复方向和批量；
- Add/Remove：保持 crash/restart 生命周期一致，调整节点和故障持续窗口。

规则必须输出已有 Mutator 能表达的合法候选，不生成任意新 trace。

## Phase D：自适应 operator 选择

### 目标

用低成本策略根据历史成功率选择 mutation operator，为 AI 提供强对照。

上下文可包含：

- Event phase 和 model action；
- Origin step/choice；
- DeliveryOrdinal/DeliveryCount；
- 最近 operator 的新状态收益；
- 父 trace 是否稳定重现目标状态。

奖励至少同时报告：

```text
new states per execution
new states per second
target-prefix survival
```

## Phase E：AI 候选排序（可选）

### 前提

只有 Phase B 明确优于 trace 级全局随机变异后才实施。

### 输入

- 新状态表示或结构化差异；
- 直接触发 event、Origin 和局部窗口；
- 确定性代码生成的合法候选列表；
- 各 operator 历史表现。

### 输出

AI 只返回候选 ID 排序及简短理由，不直接生成完整调度 trace。

### 成本记录

- 调用次数；
- 输入/输出 token；
- 推理延迟；
- 无效响应率；
- 候选排序带来的净覆盖收益。

## 8. 对照配置

需要以下分层配置，否则无法判断收益来源：

| 配置            | 归因 | 变异位置 | operator 选择     |
| --------------- | ---- | -------- | ----------------- |
| M0 原 ModelFuzz | 关闭 | 全局     | 原随机策略        |
| M1 归因但不消费 | 开启 | 全局     | 原随机策略        |
| M2 局部随机     | 开启 | 局部     | 随机              |
| M3 模型语义     | 开启 | 局部     | 确定性规则        |
| M4 自适应       | 开启 | 局部     | 在线历史表现      |
| M5 AI 排序      | 开启 | 局部     | AI 对固定候选排序 |

M1 用于隔离前缀探测开销；M2 用于隔离位置反馈收益；M3/M4 用于判断 AI 是否提供超出普通算法的价值。

## 9. 评价指标

### 9.1 归因质量

- `located / new states`；
- `failed / new states`；
- `missing origin / located`；
- initial state 比例；
- 每个新状态的首次 EventIndex；
- 每个 choice 关联的 event/new-state 数；
- 每个批次的 novelty ordinal 分布；
- 同 trace 重放的 state-key 序列一致率；
- 同 trace 重放的 attribution index 一致率。

### 9.2 搜索效率

- unique model states / executions；
- unique model states / second；
- unique state traces；
- 每 1000 次 mutation 产生新状态的 child 数；
- 父 trace 目标状态在 child 中的保持率；
- duplicate/no-op/无法应用 mutation 比例；
- checker bug 数和首次发现时间。

### 9.3 系统开销

- TLC 请求总数；
- 每条 interesting trace 的额外前缀请求数；
- prefix cache hit rate；
- 完整模型执行与 attribution 时间；
- 总 wall-clock 时间；
- AI 或自适应策略自身时间。

## 10. 公平性与重复性

- 所有对照使用相同模型、节点数、请求数、horizon、seed 集合和执行预算；
- 同时报告按 episode 和按 wall-clock 归一化的结果；
- attribution 的额外 TLC 请求不能从时间结果中隐藏；
- SUT 执行数分别报告 seed generation、seed replay、mutation 和 random，不能只用 episode 代替总执行数；
- M0/M1 区分算法收益和 instrumentation 开销；
- Pilot 使用 5 个固定 seed；正式结果目标为至少 20 个固定 seed；
- 报告中展示单次曲线、均值/中位数、方差或置信区间；
- 不只报告最终覆盖率，还报告达到共同覆盖阈值所需的执行数和时间；
- live smoke 不计入正式结果。

## 11. 决策规则

这些规则用于防止研究范围因结果不理想而无边界扩张。

1. 若真实 Fuzzer 的 attribution located rate 很低，先定位 mapper/model/origin 缺口，不直接加入 AI。
2. 若同 trace 的 state key 或 attribution index 不稳定，先解决可重复性，不声称因果或局部指导有效。
3. 若 M2 在执行数和时间两个维度都不能稳定优于 M1，不进入 AI 阶段；重新检验局部性假设。
4. 若 M3 或 M4 已达到 M5 的效果，则不把 LLM 作为核心贡献。
5. 若 M5 的覆盖收益不能抵消推理时间，则只作为负结果或辅助分析。
6. 若 enhanced 模型中的新增覆盖主要由 `currentActive` 组合贡献，必须分别报告含/不含故障状态的覆盖，避免把简单故障组合误认为协议深度。
7. 不因单次 seed、单个 bug 或单条成功 trace 改变总体结论。

## 12. 已知风险

- TLA+ 模型定义直接决定反馈质量，模型缺失动作会让实现事件被静默丢弃；
- `abstract` 参数会折叠部分 term、candidate 和 vote 状态；
- fingerprint 是状态标识，不表达状态之间的语义距离；
- 前缀探测依赖同一 trace 从相同初始状态确定执行；
- 一个新状态的直接 transition 不等于其完整因果来源；
- 一个 Node choice 可投递多条消息，观察粒度可能高于实际 mutation 粒度；
- fixed Step 会把 Tick 放在批量投递之后，不能把单消息 Step 与原批量 Step 直接等价比较；
- attribution 可能提高每个 episode 的质量，但降低单位时间吞吐量；
- 当前模型边界较小，覆盖饱和可能过快。

## 13. 结果目录约定

正式实验统一使用：

```text
results/
  phase-a/
  phase-b/
  phase-c/
  phase-d/
  phase-e/
```

每个 phase 下按配置名和 seed 分目录，至少保存：

```text
command.txt
config.json
coverage.json
attribution.json
stats.json
traces/（仅按实验需要开启）
```

不在不同 guider、run 或 seed 之间复用同一个 trace 输出目录。

## 14. 当前实验结论与下一步

Phase A 已完成逐事件 transition provenance。服务端保留原始 `DefaultStateAbstractor`
和 `RaftStateAbstractor` 行为以及 `states/keys` 接口，客户端优先使用 provenance，
对旧服务端保留前缀探测兼容路径。原模型5-seed pilot 中，所有非初始新状态均成功
定位，未出现 failed、missing-origin、event-index 或 post-state 不一致。

第一版 Phase B 把原始三个随机 Mutator 的候选限制到新状态 Origin 最近的 choices；
随后又运行了50%和70%局部调用比例的混合诊断。5-seed 最终状态均值为：

```text
global       51.8
mixed-50     48.2
mixed-70     44.6
localized    38.6
```

因此当前结果不支持“按 step 距离限制随机候选”这一位置局部性假设。该实现和混合参数
保留为负基线，但不继续搜索窗口或混合比例，也不据此进入 AI 阶段。

下一项候选实验应先更新本文档并单独实施：冻结首次进入新状态之前的执行前缀，继续在
完整后缀中使用原始随机变异。关系感知变异作为后续独立消融，优先研究消息投递与 Tick、
消息与消息、crash/restart 与关键消息、client request 与 leader 形成之间的先后关系。
这些实验不把每个实现 action 改成新的原子控制单元，也不额外增加每个候选的 TLC 执行次数。

## 15. 变更规则

后续如果需要改变以下任何内容，先更新本文档再修改代码：

- 研究问题或主要贡献；
- fixed Step 或批量投递语义；
- TLC 服务端协议；
- TLA+ 模型动作语义；
- 测试对象或目录范围；
- 对照配置；
- AI 的角色从“候选排序”扩大为“直接生成 trace”。

普通 bug 修复、日志增强、测试补充和不改变实验语义的重构可以直接实施，但必须在结果说明中记录。
