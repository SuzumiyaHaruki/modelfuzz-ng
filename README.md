# ModelFuzz-NG

ModelFuzz-NG 是一个正在从零实现的、面向分布式系统的模型引导模糊测试框架。
当前目标是先跑通 etcd-raft 的最小闭环：高层 Plan 在线解析为 Concrete Action，
Runtime 控制逻辑时间和消息队列，Adapter 驱动真实 Raft，随后把 Trace 映射到
轻量 TLA+ 模型。

## 当前模块

- `internal/core`：协议无关的 Action、Effect、Observation、Message 和 Trace。
- `internal/runtime`：单次可重放执行、逻辑时钟、消息队列和资源预算。
- `internal/plan`：高层 Plan 及其基于当前状态的在线解析。
- `internal/engine`：Plan、Runtime、模型映射和模型执行的单次闭环编排。
- `internal/adapters/etcdraft`：etcd-raft 3.7 的最小集群适配器。
- `internal/model`：Concrete Transition 到模型事件的映射及 TLC 客户端。
- `cmd/modelfuzz-ng`：读取配置和 Plan、执行轨迹并保存产物的命令行入口。
- `models/raft`：首版轻量 Raft TLA+ 模型。
- `docs`：Timer 设计与目标目录结构。

## 本地依赖

当前 `go.mod` 使用：

```go
replace go.etcd.io/raft/v3 => ../raft
```

因此 `modelfuzz-ng` 和修改后的 `raft` 目录需要同级放置。Raft 应基于 `v3.7.0`
（release 3.7），并包含 Adapter 所需的实例级 `Config.Rand` 注入接口。Raft fork
尚未发布前，仅克隆本仓库不能独立编译；这是当前阶段的已知部署约束。

## 运行最小闭环

不连接 TLC 时，CLI 仍会执行真实 Raft 并生成模型事件：

```bash
go run ./cmd/modelfuzz-ng run \
  -config examples/config.json \
  -plan examples/plans/election.json \
  -output runs/election-local
```

使用原 ModelFuzz 的 `tlc-controlled` 源码树时，可从其目录启动服务：

```bash
java -cp 'class:lib/*:lib/gson/*' tlc2.TLCServer \
  -mapperparams 'name=raft;port=2023' \
  /path/to/modelfuzz-ng/models/raft/raft.tla \
  -config /path/to/modelfuzz-ng/models/raft/raft.cfg
```

`name=raft` 不能省略：模型文件名是小写 `raft`，旧服务否则会选择默认 Mapper，
造成事件被错误映射但 HTTP 请求仍然成功。服务启动后，CLI 增加：

```bash
-tlc http://127.0.0.1:2023
```

每次运行必须使用一个尚不存在的输出目录，CLI 不会覆盖旧轨迹。目录中包含
解析结果、Concrete Action、Trace、模型事件、模型状态以及汇总结果。当前轻量
Raft 模型还不支持 crash/restart、snapshot、membership change 和多 entry
`MsgApp`；包含这些语义的轨迹会明确以 `mapping_failed` 结束。

## 当前能力声明

这里的“可执行”表示 Runtime/Adapter 能驱动真实 etcd-raft；“可映射”表示当前
`models/raft/raft.tla` Profile 能接收对应语义。模型引导实验只能使用两列都支持
的能力。

| 能力 | Runtime/Adapter | 当前模型与Mapper | 说明 |
|---|---|---|---|
| Deliver/Drop/Duplicate | 支持 | 支持 | Drop/Duplicate 只改变受控网络，对模型是 stutter |
| AdvanceTime/自然超时 | 支持 | 支持 | 一单位时间对应每个存活节点一次 Tick |
| 强制选举超时 | 支持 | 支持 | 自然/强制来源都映射为 `Timeout` |
| Client Request | 支持 | 支持 | 请求值限十进制 `1..MaxValue` |
| crash/restart | 支持 | 不支持 | Engine 在修改真实节点前返回 `unsupported_by_model` |
| snapshot/membership change | Adapter 有部分处理 | 不支持 | 需要扩展独立模型 Profile |
| PreVote/CheckQuorum | 当前关闭 | 不支持 | 启用 Raft 配置前必须先补模型 |

消息分类：

| 消息 | 当前处理方式 |
|---|---|
| `MsgVote`、`MsgVoteResp` | 映射为选举模型动作 |
| `MsgApp`、`MsgAppResp` | 映射为复制和确认动作；`MsgApp` 当前只允许零或一条 entry |
| `MsgHeartbeat` | 映射为无 entry 的 `MsgApp`，保留 term、角色和 commit 传播 |
| `MsgHeartbeatResp` | 当前 Profile 中明确 stutter |
| `MsgReadIndex`、`MsgReadIndexResp` | 只读状态未进入模型，明确 stutter |
| `MsgSnap`、`MsgTimeoutNow`、`MsgPreVote` 等其他网络消息 | 不支持并返回错误，不会静默忽略 |
| `MsgHup`、`MsgBeat`、`MsgProp` 等本地消息 | 不进入 Runtime 网络队列 |

示例 Plan：

- `election.json`：只完成节点 1 的选举；
- `election-commit-node1.json`：节点 1 当选并把 no-op 复制到节点 2后提交；
- `election-commit-node2.json`：节点 2 当选并把 no-op 复制到节点 3后提交；
- `client-request-commit.json`：节点 1 当选，随后复制并提交 no-op 和请求值 `1`。

上述三条完整 Plan 已使用真实 etcd-raft 和 controlled TLC 运行，结果见
[`docs/experiments/basic-raft-20260720.md`](docs/experiments/basic-raft-20260720.md)。

严格重放已有运行时，默认从 `trace.json` 同目录读取 `config.json`：

```bash
go run ./cmd/modelfuzz-ng replay \
  -trace runs/basic-raft-20260720/client-request-commit/trace.json \
  -output runs/replay-client-request
```

Replay 会逐步检查逻辑时间、MessageID/Link/Position、Effect、节点快照和
ObservationDigest，并在第一处差异停止。三条完整示例的实际重放分别匹配
`6/6`、`6/6` 和 `9/9` 个步骤。

## 验证

```bash
gofmt -w internal cmd
go mod tidy
go test ./...
go test -race ./...
go vet ./...
```

详细结构和后续模块见 [`docs/project-structure.md`](docs/project-structure.md)，
时间与超时语义见 [`docs/timer-design.md`](docs/timer-design.md)。
