# 协议耦合审计（2026-07-22）

## 范围与结论

本审计只分析当前代码能否从进程内 etcd-raft 演进为可适配 RedisRaft、
CometBFT、HotStuff 等系统的框架，不新增 membership、PreVote 或其他 Raft
功能，也不在本阶段重构公共接口。

结论分为两部分：

1. 项目已经具有可复用的执行内核。`core`、`runtime`、`engine`、Trace、
   Corpus、checkpoint、minimizer 和 Mapper/Executor/Oracle 接口没有依赖
   etcd-raft 包，可以继续保留。
2. 当前产品入口仍是一个“etcd-raft 专用装配”。CLI/config、在线随机与 LLM
   policy、语义覆盖投影、快照指标以及 strict TLC 的 Java 事件绑定都直接理解
   Raft。第二个协议不能只新增一个 Adapter；它还需要修改多个现有通用包。

因此当前最准确的定位是：**具有通用执行内核的 etcd-raft 深度实现**，尚不是
已经验证的多协议 ModelFuzz 平台。迁移不需要推倒重写，但应先建立协议插件边界。

## 当前依赖形态

```text
cmd/modelfuzz-ng
  ├── etcdraft.Adapter
  ├── raft.Mapper / raft.Profile
  ├── raft.Oracle
  ├── raft.ProjectCoverage
  ├── raft-aware Random / LLM / directed policies
  └── TLC Raft bounds/profile validation
          │
          ▼
engine ─ runtime ─ sut.Adapter
  │         │
  │         └── deterministic network / logical time / trace
  └── model.Mapper / model.Executor / oracle.Checker
          │
          ▼
experiment / corpus / mutation / persistence / minimizer
```

下半部分已经通过接口隔离；最上层装配和若干反馈组件仍把 Raft 语义写死。

## 分层审计

| 区域 | 当前可复用程度 | 证据与限制 |
| --- | --- | --- |
| `internal/core` | 高 | Action、Message envelope、Observation、Effect、Trace 不导入 Raft；`Semantic`/`ModelEvent.Params` 为不透明 JSON 数据 |
| `internal/sut` | 中高 | Adapter 接口不导入 Raft；但能力集合和方法集合是封闭的，默认同步、可 Tick、可直接 Deliver 的节点模型 |
| `internal/runtime` | 高（进程内） | 确定性队列、Drop/Duplicate/Partition、逻辑时间和重放均不解释消息语义；但要求所有协议消息都能被 Adapter 完整截获 |
| `internal/plan` | 中高 | 网络、时间、Crash/Restart、Request 对消息驱动系统通用；无法表达协议专用 admin、磁盘或签名故障而不扩展联合类型 |
| `internal/engine` | 高 | 只依赖 Resolver、Mapper、Executor、Oracle 和 Runtime 接口 |
| `internal/model` | 高 | Event、Mapper、Profile、Executor 是通用边界 |
| `internal/oracle` | 中高 | Checker 接口通用；`Finding.Term` 是 Raft 概念泄漏 |
| Corpus/checkpoint/minimizer | 高 | 主要处理 Plan、状态键、失败签名和 JSON 产物，不解释 Raft 状态 |
| `internal/mutation` | 中高 | 基础变异只依赖通用 Plan；`MaxValue` 和数字字符串 Request 仍来自当前 Raft 测试负载约定 |
| `internal/policy` | 低 | Random 直接创建 `raft.Mapper` 并读取 `role/term/leader/last_index`；LLM prompt 描述 MsgProp、term/log bounds；快照策略完全是 Raft 专用 |
| `internal/metrics` / experiment report | 中低 | 通用 action/effect/message 计数可复用，但同一结构写死 snapshots、compaction、`raft.snapshot_*` |
| `internal/model/tlc` | 中 | HTTP Execute 传输本身通用；health bounds 写死 `LargestTerm/MaxLogIndex/Server/MaxValue/Nil/model_profile` |
| `tools/tlc-server` | 低 | `RaftEventMapper`、动作白名单、参数名和 `ModelBounds` 都按 Raft/TLA 常量写死 |
| CLI/config | 低 | `cliConfig` 直接包含 `raftSettings` 和 `raftmodel.Config`，`buildEngine` 固定创建 etcdraft/raft mapper/raft oracle |
| etcdraft/raft model/raft oracle | 协议专用（合理） | 这些组件本来就应属于 Raft 插件，不应为了形式上的通用而削弱其强类型语义 |

## 主要发现

### P0：第二协议接入前必须解决

#### 1. 缺少协议装配边界

`buildRuntime` 和 `buildEngine` 固定实例化 etcd-raft Adapter、Raft Mapper、
Raft Oracle；CLI 的配置校验也固定比较 Raft nodes、term/log bounds 和 snapshot
profile。新增协议必须修改主命令，而不是注册一个实现。

建议引入一个装配层，而不是让 Engine 认识协议：

```text
ProtocolBundle
  ├── AdapterFactory
  ├── Mapper + Profile
  ├── Oracle factories
  ├── ActionSource / planner factory
  ├── CoverageProjector
  ├── MetricsCollector
  └── Model contract / bounds validator
```

CLI 只选择 bundle 并加载其专用配置。现有 etcd-raft 组件整体迁入第一个 bundle，
不改变其行为。

#### 2. “通用随机策略”实际上依赖 Raft 模型

`internal/policy/random.go` 直接导入 `internal/model/raft`，通过 Raft Profile
筛选 Deliver，并读取：

- `role`、`term`、`leader`、`last_index`；
- `LargestTerm`、`MaxLogIndex`、`MaxValue`；
- Leader no-op 和 follower `MsgProp` 约定。

应拆为：

- 通用候选生成：消息、时间、Crash/Restart、Partition/Heal；
- 协议提供的 `ActionFilter` 和 `RequestGenerator`；
- Raft 专用的选举冷却、模型边界和定向 snapshot policy。

无需把 CometBFT 的 round 或 HotStuff 的 QC 塞进通用 RandomConfig。

#### 3. 反馈覆盖和指标存在反向依赖

Experiment 已经允许注入 `CoverageProjector`，这是正确边界；但 CLI 固定注入
`raftmodel.ProjectCoverage`。同时 `metrics.RunMetrics` 和 Experiment Report 在
通用结构中写死 snapshot/compaction 字段和 `raft.snapshot_*` 事件名。

建议保留通用计数：

- Action/Effect/MessageType/ModelEvent/Oracle/Termination；
- queued message、duration、coverage 和 corpus 指标。

协议指标由 collector 插件输出带 namespace 的结构，例如：

```json
{
  "protocol_metrics": {
    "raft": {"snapshots_sent": 2},
    "cometbft": {"round_changes": 4}
  }
}
```

#### 4. strict TLC 服务是 Raft 事件执行器

Go `model.Executor` 接口通用，但当前 Java 服务中的以下部分不是：

- `RaftEventMapper` 的事件 switch 和动作白名单；
- `ModelBounds` 对 Raft 常量和参数名的解释；
- health 中的 `model_profile`、term/log/value bounds；
- storage-snapshot 专用动作识别。

第二协议需要可插拔的事件 schema，或由模型旁边的 manifest 声明：

- model ID 和 schema version；
- event name -> TLA operator；
- 参数重命名和类型；
- 任意模型常量/bounds；
- 可用 invariants。

否则每接一个协议都要修改并重新发布 Java 服务。

### P1：从内存库迁移到真实进程前必须解决

#### 5. Adapter 合同隐含“同步、可暂停、全消息可见”

Runtime 假设一次 `Deliver/Tick/Request` 同步返回全部 Effects，所有出站协议消息
都进入其确定性队列。真实 RedisRaft/CometBFT 具有 socket、后台线程、磁盘和
异步回调，不能天然满足该假设。

应将两个维度分开：

```text
Protocol plugin：理解消息和状态语义
Execution backend：in-process / external-process / proxy-instrumented
```

外部 backend 至少需要：

- 节点进程启动、停止、重置和工作目录隔离；
- 网络代理或源码 hook，形成可确认的消息 barrier；
- 等待系统 quiescent 的机制，而不是假定方法返回即稳定；
- WAL/snapshot 文件生命周期和恢复边界；
- 将 wall-clock timer 转换为可控逻辑事件，或明确标记非确定性。

#### 6. Message Payload 只适合当前重执行式重放

`core.Message.Payload` 是不写入 Trace 的 `any`，Runtime 只保存 digest 和
metadata。当前 replay 通过相同 seed 重建 Adapter 和队列，因此可工作；外部系统
若无法从头确定性重建 wire payload，就不能仅凭 Trace 独立重放。

后续可选择：

- 保存经过脱敏、稳定编码的协议 wire bytes；或
- 保存 Adapter 可解析的 durable payload token，并由 artifact store 管理内容。

这不是要求把所有 payload 放进通用模型，只是保证外部进程反例可携带和重放。

#### 7. Action/Capabilities 是封闭联合类型

当前基础 Action 足以测试普通消息驱动共识，但无法自然增加：

- membership/admin command；
- disk write/drop/corrupt/fsync；
- CometBFT ABCI/mempool 操作；
- HotStuff key/QC/pacemaker 故障。

不应不断扩大全局 enum。建议保留现有基础 Action，再增加一个受 schema 校验的
`invoke(namespace, name, payload)` 扩展点；Resolver、minimizer 和 persistence
把它作为不透明但可复制的 Action，具体协议或 backend 解释其含义。

#### 8. Semantic map 是无版本的隐藏接口

补充：正式 v1 Raft coverage projector 使用 `raft-coverage-v1`，并写入实验配置指纹；
checkpoint v1 会拒绝不同 coverage schema。这里指出的 Observation
`Semantic map` 协议/backend schema 仍未版本化，不能与 coverage projector 的版本
保护混为一谈。

动态 `map[string]any` 避免了 core 对 Raft 的编译依赖，但 Mapper、Oracle、Policy
仍共同依赖字符串字段。这种耦合在编译期不可见，字段变更只能运行时失败。

建议 Observation 增加：

- protocol/backend 标识；
- semantic schema namespace 和 version；
- Adapter 声明的 capability/schema manifest。

插件内部仍可使用强类型 view 解码，不要求 core 理解字段。

### P2：组织与接口债务

- `oracle.Finding.Term` 应替换为通用 `Evidence map[string]any`，Raft Oracle 可在
  Evidence 中保存 term；保留 Node/Step 作为通用定位字段。
- `metrics` 包的注释声称不解释 Raft，但代码解释 `raft.snapshot_*`，应拆分后修正。
- Raft 定向策略与通用 Random/LLM 位于同一 package，依赖边界不清晰。
- TLC health 应返回通用 `model_id/schema_version/bounds`，Raft bounds 是其内容，
  而不是固定顶层字段。
- 进程 crash 当前只保留 Adapter 定义的稳定状态；不能把它等同于真实 OS crash、
  fsync 或 partial write。

## 本轮 snapshot-status 修复对审计的启示

随机种子 `210088` 同时命中了以下真实 etcd-raft 行为：

1. 旧 snapshot 传输期间 Leader 继续创建更新 snapshot；
2. 延迟 `MsgAppResp` 使 Match 前进，但仍不足以越过当前 Storage first index；
3. `SnapshotFinish` 使用 `max(Match+1, PendingSnapshot+1)`；
4. 旧 `leaderCommit` 不得使 follower commit 回退；
5. 目标 crash 不阻止 Leader 生成并排队 MsgSnap；
6. pending snapshot 期间的 stale reject response 可以被忽略。

这些错误都不是 Action 覆盖不足，而是具体实现状态与抽象模型之间的信息差。
特别是当前基础模型没有显式的 Progress state（Probe/Replicate/Snapshot），同一个
`MsgAppResp` 在不同 progress state 下可能采用不同 Next 规则。继续增加协议功能前，
应优先保证现有抽象不会把合法乱序轨迹误判为模型失败。

## 推荐的无功能扩展路线

### 阶段 A：保持行为不变，建立边界

1. 增加 `ProtocolBundle`/registry，由 CLI 选择 bundle。
2. 将 etcdraft Adapter、Raft Mapper/Profile、Oracle、coverage、policy、metrics
   作为一个 bundle 装配。
3. 通用包停止导入 `internal/model/raft`，停止匹配 `raft.*` 和 `Msg*` 字符串。
4. 配置文件拆成通用 execution/model transport 与 protocol-specific 两段。

### 阶段 B：分离执行 backend

1. 保留现有 in-process backend 作为快速确定性基线。
2. 定义 external-process lifecycle、message interception 和 quiescence 合同。
3. 增加可持久化 payload/token，保证外部反例独立 replay。

### 阶段 C：用第二实现验证，不先追求万能模型

优先以 RedisRaft 验证 external-process、网络和持久化边界。它仍属于 Raft，能够
复用安全性质，同时足以暴露内存 Adapter 假设。之后再用 CometBFT 或 HotStuff
验证真正的跨协议边界；每个协议保留自己的 Mapper、模型和 Oracle。

## 验收标准

在宣称“多协议框架”前，至少满足：

- `core/runtime/engine/experiment` 不导入任何具体协议包；
- 通用 policy/metrics 不读取 `role/term/commit`，不匹配 `MsgApp/MsgSnap`；
- CLI 可通过 registry 选择协议，不修改 `buildEngine` 主流程；
- strict 模型服务按 schema/manifest 绑定事件，而不是固定 Raft switch；
- 一个最小非 Raft 测试插件可以走通 Plan -> Runtime -> Trace -> Mapper -> Oracle；
- 一个外部进程 backend 可以稳定 reset、拦截消息、crash/restart 和 replay；
- etcd-raft 现有实验、随机种子和 strict TLC 结果保持不变。

在这些边界建立之前，暂停增加 PreVote、membership 等细小 Raft 功能是合理的。
