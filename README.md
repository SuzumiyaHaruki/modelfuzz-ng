# ModelFuzz-NG

ModelFuzz-NG 是一个正在从零实现的、面向分布式系统的模型引导模糊测试框架。
当前目标是先跑通 etcd-raft 的最小闭环：高层 Plan 在线解析为 Concrete Action，
Runtime 控制逻辑时间和消息队列，Adapter 驱动真实 Raft，随后把 Trace 映射到
轻量 TLA+ 模型。

## 当前模块

- `internal/core`：协议无关的 Action、Effect、Observation、Message 和 Trace。
- `internal/runtime`：单次可重放执行、逻辑时钟、消息队列和资源预算。
- `internal/plan`：高层 Plan 及其基于当前状态的在线解析。
- `internal/adapters/etcdraft`：etcd-raft 3.7 的最小集群适配器。
- `internal/model`：Concrete Transition 到模型事件的映射及 TLC 客户端。
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

## 验证

```bash
gofmt -w internal
go mod tidy
go test ./...
go test -race ./...
go vet ./...
```

详细结构和后续模块见 [`docs/project-structure.md`](docs/project-structure.md)，
时间与超时语义见 [`docs/timer-design.md`](docs/timer-design.md)。
