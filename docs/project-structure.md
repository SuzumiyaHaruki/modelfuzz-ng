# ModelFuzz-NG 目录结构参考

## 1. 文档用途

本文记录 ModelFuzz-NG 的当前目录和目标目录结构，作为后续分模块开发时的参考。目录划分不是一次性固定的，可以随着设计和实现进展持续修改。

状态标记：

- `已有`：当前已经存在；
- `近期`：完成基础执行链路需要优先增加；
- `后续`：第一版可以不实现；
- `可选`：是否需要取决于后续复杂度。

## 2. Workspace关系

开发阶段建议保持两个同级目录：

```text
Desktop/
├── modelfuzz-ng/       ModelFuzz-NG自身代码
└── raft/               etcd-raft v3.7及必要的受控修改
```

`raft` 不放入 `modelfuzz-ng` 内部。当前 `go.mod` 使用：

```go
replace go.etcd.io/raft/v3 => ../raft
```

这样可以分别维护测试框架和被测 Raft 源码，也能清楚区分 ModelFuzz-NG 的修改与 Raft 自身的修改。以后如果使用发布版本或独立 fork，可以再调整 `replace`。

## 3. 当前实际目录

```text
modelfuzz-ng/
├── cmd/
│   └── modelfuzz-ng/
│       ├── config.go
│       ├── main.go
│       ├── main_test.go
│       └── output.go
├── docs/
│   ├── experiments/
│   │   └── basic-raft-20260720.md
│   ├── project-structure.md
│   └── timer-design.md
├── internal/
│   ├── adapters/
│   │   └── etcdraft/
│   │       ├── adapter.go
│   │       ├── adapter_test.go
│   │       ├── cluster.go
│   │       ├── config.go
│   │       ├── message.go
│   │       ├── node.go
│   │       ├── observation.go
│   │       ├── random.go
│   │       ├── ready.go
│   │       └── timeout.go
│   ├── core/
│   │   ├── action.go
│   │   ├── action_test.go
│   │   ├── doc.go
│   │   ├── effect.go
│   │   ├── effect_test.go
│   │   ├── error.go
│   │   ├── id.go
│   │   ├── id_test.go
│   │   ├── message.go
│   │   ├── message_test.go
│   │   ├── observation.go
│   │   ├── observation_test.go
│   │   ├── timer.go
│   │   ├── timer_test.go
│   │   ├── trace.go
│   │   └── trace_test.go
│   ├── model/
│   │   ├── event.go
│   │   ├── event_test.go
│   │   ├── executor.go
│   │   ├── mapper.go
│   │   ├── transition.go
│   │   ├── raft/
│   │   │   ├── mapper.go
│   │   │   └── mapper_test.go
│   │   └── tlc/
│   │       ├── client.go
│   │       ├── client_test.go
│   │       └── protocol.go
│   ├── plan/
│   │   ├── action.go
│   │   ├── action_test.go
│   │   ├── resolver.go
│   │   ├── resolver_test.go
│   │   ├── result.go
│   │   ├── selector.go
│   │   └── sequence.go
│   ├── engine/
│   │   ├── doc.go
│   │   ├── engine.go
│   │   ├── engine_test.go
│   │   ├── error.go
│   │   └── result.go
│   ├── runtime/
│   │   ├── action.go
│   │   ├── clock.go
│   │   ├── network.go
│   │   ├── network_test.go
│   │   ├── recorder.go
│   │   ├── runtime.go
│   │   └── runtime_test.go
│   └── sut/
│       └── adapter.go
├── examples/
│   ├── config.json
│   └── plans/
│       ├── client-request-commit.json
│       ├── election-commit-node1.json
│       ├── election-commit-node2.json
│       └── election.json
├── models/
│   └── raft/
│       ├── README.md
│       ├── raft.cfg
│       └── raft.tla
├── .gitignore
├── go.mod
└── go.sum
```

当前阶段已经完成协议无关的 core 数据模型、最小 SUT 接口、基础 Runtime、
可通过 Runtime 端到端运行的 `internal/adapters/etcdraft` 最小适配器，以及
Concrete Transition 到 Raft TLA+ 事件的映射、TLC HTTP 客户端，以及
不依赖 LLM 的首版 Plan 数据结构和 Resolver。当前也已经具备 Engine 和 CLI，
可以把 JSON Plan、真实 Raft、模型事件映射和可选的 controlled TLC 串成一次
完整执行，并持久化全部中间产物。

## 4. 建议的目标目录

下面是当前建议的整体结构。近期不需要一次创建所有空目录，应当实现到对应模块时再增加。

```text
modelfuzz-ng/
├── cmd/                              # 已有：可执行程序入口
│   └── modelfuzz-ng/
│       ├── main.go                   # run子命令和依赖组装
│       ├── config.go                 # JSON配置及Raft/模型对齐检查
│       └── output.go                 # 运行产物安全写入
│
├── configs/                          # 近期：可复用运行配置
│   ├── etcdraft-basic.yaml
│   └── experiments/                  # 后续：实验参数
│
├── docs/                             # 设计、使用和实验文档
│   ├── project-structure.md          # 本文档
│   ├── timer-design.md               # Timer设计讨论
│   ├── architecture.md               # 近期：整体组件和数据流
│   └── trace-format.md               # 后续：Plan/Action/Trace格式
│
├── internal/
│   ├── core/                         # 已有：协议无关的数据模型
│   │   ├── action.go
│   │   ├── effect.go
│   │   ├── error.go
│   │   ├── id.go
│   │   ├── message.go
│   │   ├── observation.go
│   │   ├── timer.go
│   │   ├── trace.go
│   │   └── *_test.go
│   │
│   ├── sut/                          # 已有：被测系统的通用接口
│   │   └── adapter.go                # Adapter接口和能力声明
│   │
│   ├── adapters/                     # 具体被测系统的Adapter
│   │   └── etcdraft/                 # 已有：etcd-raft v3.7 Adapter
│   │       ├── adapter.go            # Adapter总体实现和sut.Adapter接口断言
│   │       ├── config.go             # Raft节点数、tick、随机种子等配置
│   │       ├── cluster.go            # 集群创建、重置和整体状态
│   │       ├── node.go               # 单节点RawNode和MemoryStorage封装
│   │       ├── random.go             # 按Seed、NodeID和Epoch派生节点独立随机流
│   │       ├── ready.go              # Ready/Advance及持久化处理
│   │       ├── message.go            # raftpb.Message与core.Message转换
│   │       ├── timeout.go            # 自然/强制timeout处理和事件记录
│   │       ├── observation.go        # Raft状态转成core.Observation
│   │       └── adapter_test.go       # Runtime端到端选举、提交和重启测试
│   │
│   ├── runtime/                      # 已有：执行Concrete Action
│   │   ├── runtime.go                # 单次执行状态和入口
│   │   ├── action.go                 # Concrete Action执行
│   │   ├── clock.go                  # LogicalTime和AdvanceTime
│   │   ├── network.go                # 按Link维护确定性消息队列
│   │   ├── recorder.go               # Action、Effect和Trace记录
│   │   └── *_test.go
│   │
│   ├── model/                        # 已有：Concrete Transition到模型状态
│   │   ├── event.go                  # 模型事件及Reset协议
│   │   ├── transition.go             # 动作前后Observation和StepRecord
│   │   ├── mapper.go                 # 通用Mapper接口
│   │   ├── raft/                     # Raft协议语义映射
│   │   └── tlc/                      # Controlled TLC HTTP客户端
│   │
│   ├── plan/                         # 已有：生成来源无关的高层计划
│   │   ├── action.go                 # PlanAction及批量动作
│   │   ├── sequence.go               # PlanSequence
│   │   ├── selector.go               # Link、Start、Count消息Selector
│   │   ├── result.go                 # resolved/partial/skipped等解析结果
│   │   ├── resolver.go               # PlanStep在线解析为Concrete Action
│   │   └── *_test.go
│   │
│   ├── engine/                       # 已有：单条Plan完整执行编排
│   │   ├── engine.go                 # Plan到模型执行的主循环
│   │   ├── result.go                 # 状态和可持久化执行结果
│   │   ├── error.go                  # 分层错误分类
│   │   └── *_test.go
│   │
│   ├── policy/                       # 计划生成和调度策略
│   │   ├── policy.go                 # 统一接口
│   │   ├── random.go                 # 近期：基础随机策略
│   │   ├── fair.go                   # 后续：有限公平/消息年龄策略
│   │   └── llm/                      # 后续：LLM Plan生成
│   │       ├── planner.go
│   │       ├── prompt.go
│   │       ├── schema.go
│   │       └── client.go
│   │
│   ├── oracle/                       # 系统正确性和模型检查
│   │   ├── oracle.go                 # Oracle统一接口
│   │   ├── invariant.go              # 通用不变量组合
│   │   ├── model.go                  # 使用model包结果进行判定
│   │   └── raft.go                   # Raft专用Oracle，可选后移到Adapter
│   │
│   ├── trace/                        # Trace的算法和持久化，不放数据定义
│   │   ├── io.go                     # JSON读写
│   │   ├── replay.go                 # Concrete replay
│   │   ├── compare.go                # Effect/状态差异比较
│   │   ├── minimize.go               # 后续：失败轨迹缩减
│   │   └── *_test.go
│   │
│   ├── corpus/                       # 后续：有价值Plan/Trace集合
│   │   ├── corpus.go
│   │   ├── select.go
│   │   ├── mutate.go
│   │   └── *_test.go
│   │
│   ├── metrics/                      # 后续：覆盖率和性能统计
│   │   ├── metrics.go
│   │   └── reporter.go
│   │
│   └── coordinator/                  # 可选：并行worker和任务协调
│       ├── coordinator.go
│       └── worker.go
│
├── models/                           # TLA+模型及映射配置
│   └── raft/
│       ├── README.md
│       ├── raft.tla
│       └── raft.cfg
│
├── scripts/                          # 构建、运行和实验辅助脚本
│   ├── test.sh
│   └── run-etcdraft.sh
│
├── testdata/                         # 小型固定Trace、Plan和期望结果
│   ├── plans/
│   └── traces/
│
├── .gitignore
├── README.md
├── go.mod
└── go.sum
```

## 5. 各模块职责

### 5.1 `internal/core`

只保存跨模块共享的协议无关数据，不实现调度算法，不导入具体 Raft 包。

当前文件职责：

| 文件               | 职责                                             |
| ------------------ | ------------------------------------------------ |
| `action.go`      | Concrete Action、MessageSelector和ActionSequence |
| `effect.go`      | Action产生的消息、timeout和模型事件Effect        |
| `id.go`          | Node、Message、Execution和Link等稳定ID           |
| `message.go`     | 协议无关消息信封                                 |
| `timer.go`       | LogicalTime和TimerFired来源                      |
| `observation.go` | 提供给Plan/Policy的当前执行视图                  |
| `trace.go`       | StepRecord和Concrete Trace数据格式               |
| `error.go`       | core数据校验错误                                 |

约束：

- 不依赖 `go.etcd.io/raft/v3`；
- 不包含 LLM、随机或公平调度逻辑；
- 不负责文件读写；
- 不解释 `TypeHint`、`RoleHint` 和 Semantic map；
- `ActionSequence` 表示 Plan 运行时逐步产生的具体动作，而不是 LLM 原始计划。

### 5.2 `internal/sut`

定义 Runtime 与具体被测系统之间的最小接口。当前接口覆盖：

- Reset；
- 每单位逻辑时间的 Tick；
- Deliver；
- 强制 Timeout；
- Crash/Restart；
- Client Request；
- Observation；
- Effect收集。

这里不能包含 etcd-raft 特有类型。`AdvanceTime` 由 Runtime 拆成多次
`Tick`；Drop 和 Duplicate 仅操作 Runtime 的消息队列，不进入 SUT 接口。

### 5.3 `internal/adapters/etcdraft`

负责把通用 Action 翻译成 etcd-raft 行为，并把 Raft 结果翻译成 core Effect。

第一版重点：

- `RawNode + MemoryStorage`；
- 三个普通 voter；
- 每单位 LogicalTime 对所有存活节点调用一轮 `Tick()`；
- 处理 Ready/Advance；
- 将 `raftpb.Message` 转换为尚未分配 ID 的 `core.Message`，交由 Runtime 注册；
- 记录自然/强制 timeout；
- PreVote、CheckQuorum 和 AsyncStorageWrites 暂时关闭；
- 每节点随机性必须可重复。

### 5.4 `internal/runtime`

负责执行一条具体 Action 或 Plan 解析出的动作，并持有一次执行期间的动态状态：

- 当前逻辑时间；
- 按 Link 划分的消息队列；
- MessageID和LinkSequence分配；
- 使用最新 Observation 检查节点运行状态；
- Action前置条件校验；
- Effect时间校验；
- Trace追加。

当前 Runtime 已实现逐 Tick 时间推进、出站消息注册、Deliver/Drop/Duplicate、
Adapter 能力与前置条件检查、Observation 合并和 Concrete Trace 记录。网络队列
暂时保留在 `runtime/network.go`；如果后续分区和连接故障逻辑明显增多，再拆为
`internal/network`。

### 5.5 `internal/plan`

保存人工、JSON、随机策略或 LLM 产生的高层、best-effort 计划。例如：

```text
Deliver(link=A->B, count=5)
AdvanceTicks(delta=2)
Timeout(node=2)
Crash(node=1)
```

PlanStep 在执行时解析为零到多个 Concrete Action：

- 队列少于请求数量时执行已有消息并返回 partial；
- 状态不满足时返回 skipped/invalid；
- Selector 在执行时解析为具体 MessageID；
- AdvanceTicks 根据当前时间解析为绝对 TargetTime；
- MaxBatch 和 MaxAdvanceTicks 限制单步展开规模。

首版 Plan 使用明确 NodeID，不包含 `AnyCandidate`、`CurrentLeader` 等动态节点
选择器。这类选择更适合在基础 Engine 跑通后，结合具体使用需求增加。

### 5.6 `internal/model`

负责把已经实际发生的 Concrete Transition 映射为形式化模型事件：

- `Transition` 同时包含动作前 Observation、StepRecord 和动作后 Observation；
- 通用 `Mapper` 不解释具体协议；
- `model/raft` 识别选举超时、实际投递的 Raft 消息、leader 变化和提交；
- `model/tlc` 发送事件序列，并接收 TLC 状态文本与 fingerprint key。

模型映射以实际 Effect 为准。例如 `ActionDeliver` 只有在 Adapter 成功接收消息并
记录 `raft.message_delivered` 后才会变成 `DeliverMessage`。Drop 和 Duplicate
只改变 Runtime 网络队列，因此可以映射为零条模型事件。

### 5.7 `internal/engine`

负责更高层的 fuzzing 生命周期：

- 创建和重置 Runtime；
- 请求 Policy 生成 Plan；
- 执行 Plan；
- 调用 Oracle；
- 收集 Coverage/Metrics；
- 将有价值的 Plan/Trace 放入 Corpus；
- 控制 iteration 和总体预算。

### 5.8 `internal/policy`

负责生成 Plan，而不是直接修改 Runtime。

建议先实现简单随机策略，验证完整执行链路；LLM 策略在 Plan schema 和 Runtime 行为稳定后加入。这样可以区分基础框架问题和 LLM 生成问题。

### 5.9 `internal/oracle`

负责判断一次执行是否违反不变量、是否进入新模型状态。协议无关组合逻辑放在这里；强 Raft 语义的检查可以放在 `oracle/raft.go`，或在 Adapter 增长后移入 `internal/adapters/etcdraft`。

### 5.10 `internal/trace`

`core.Trace` 只定义数据格式；该目录实现：

- 保存和加载；
- 严格重放；
- Effect和状态比较；
- 失败轨迹缩减。

## 6. 主要数据流

```text
Policy / LLM
    |
    v
PlanSequence
    |
    v
Plan Resolver ---- 当前Observation
    |
    v
Concrete Action
    |
    v
Runtime ---- Deterministic Network / Logical Clock
    |
    v
Adapter ---- System Under Test
    |
    v
Timed Effects + New Observation
    |
    +----> Trace Recorder
    +----> Model Mapper ----> TLC Client ----> Model States
    +----> Oracle
    +----> Coverage / Corpus / Metrics
```

自然 timer 触发不返回到 Policy 变成新的可选 Action，而是作为执行 `AdvanceTime` 时产生的 `EffectTimerFired` 记录。

## 7. 依赖方向

建议保持以下依赖方向：

```text
cmd
  -> engine
      -> plan / policy / oracle / corpus / metrics
      -> runtime
          -> internal/sut接口
          -> core

internal/adapters/etcdraft
  -> internal/sut接口
  -> core
  -> go.etcd.io/raft/v3

internal/model/raft
  -> internal/model
  -> core

internal/model/tlc
  -> internal/model

internal/plan
  -> core

core
  -> Go标准库
```

禁止的依赖包括：

- `core -> internal/adapters/etcdraft`；
- `core -> raft`；
- `runtime -> policy/llm`；
- `internal/adapters/etcdraft -> engine`；
- 为方便单个 Adapter 而把协议语义写入 core。

## 8. 建议实现顺序

### 阶段一：完成单次Raft执行

1. `internal/sut` 最小接口；
2. `internal/runtime` 的 LogicalTime 和消息队列；
3. `internal/adapters/etcdraft` 的集群创建、Tick、Ready和消息转换；
4. 执行 Deliver、AdvanceTime、Timeout、Crash、Restart、Request；
5. 产生并校验 Concrete Trace。

### 阶段二：增加Plan

1. 已完成 PlanAction和PlanSequence；
2. 已完成 batch Deliver/Drop/Duplicate；
3. 已完成 AdvanceTicks；
4. 已完成 Link/Start/Count Selector解析；
5. 已完成 resolved/partial/skipped/invalid/empty_queue解析结果；
6. 已完成 Engine 执行已解析动作并生成Concrete ActionSequence；
7. 已完成 CLI 读取JSON Plan并保存完整运行产物。

### 阶段三：形成基础fuzzer

1. 已完成单条Plan的Engine循环，待增加多轮探索调度；
2. Random Policy；
3. 基础 Oracle；
4. Coverage和Metrics；
5. Trace保存与重放；
6. 简单Corpus和Mutation。

### 阶段四：加入高级能力

1. LLM Plan生成；
2. Partition/FairDeliver/RunUntilLeader等宏；
3. 在已有TLA+执行链路上增加模型引导策略；
4. 多worker并行执行；
5. Trace minimization；
6. PreVote、CheckQuorum、snapshot等Raft扩展。

## 9. 维护规则

每次新增、删除或移动主要目录时，应同步修改本文：

1. 更新“当前实际目录”；
2. 更新目标目录树中的状态；
3. 更新模块职责和依赖方向；
4. 如果设计决定发生变化，链接或同步更新对应设计文档；
5. 不为尚未开始的模块提前创建大量空目录和空文件。

本文是参考结构，不应阻止实现阶段根据实际耦合关系合并或拆分 package。目录划分的目标是保持职责清楚、依赖单向和测试方便，而不是追求目录数量。
