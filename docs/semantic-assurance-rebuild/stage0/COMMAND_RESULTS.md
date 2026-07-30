# Stage 0 命令结果

## 1. 执行边界

- 工作目录：`/home/test/Desktop/modelfuzz-ng-semantic-assurance-rebuild-v1`
- 执行日期：2026-07-30（Asia/Shanghai）
- 本阶段没有启动 TLC 服务、fuzz campaign 或任何长时间实验。
- 为避免只读的用户级 Go cache 影响验证，Go 命令使用
  `GOCACHE=/tmp/modelfuzz-ng-stage0-gocache`。这只改变构建缓存位置，不改变
  被测源码、依赖或测试参数。

## 2. Git 与工具链

| 命令 | exit code | 关键结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `75d4e51120b370acb880d003629f916da3f1a080` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1` |
| `git status --short` | 0 | 空；开始审计时工作树干净 |
| `git diff --check` | 0 | 无输出 |
| `go version` | 0 | `go version go1.26.4 linux/amd64` |
| `go env GOMOD` | 0 | `/home/test/Desktop/modelfuzz-ng-semantic-assurance-rebuild-v1/go.mod` |
| `java -version` | 0 | OpenJDK `17.0.20`，Temurin `17.0.20+8` |

`go.mod` 中的本地替换为：

```text
replace go.etcd.io/raft/v3 => ../raft
```

`go list -m -json go.etcd.io/raft/v3` 确认有效模块版本为 `v3.7.0`，替换目录
为 `../raft`。该路径解析到 `/home/test/Desktop/raft-modelfuzz`；其状态为：

- commit：`8340725019b1376bdd322dd451395698e7d82979`
- branch：`modelfuzz-v3.7`
- `git status --short`：空

因此本次验证使用的是明确、干净的本地 etcd-raft replace，而不是网络下载的
同名模块。

## 3. 基线验证

| 命令 | exit code | 耗时 | 结果 |
|---|---:|---:|---|
| `env GOCACHE=/tmp/modelfuzz-ng-stage0-gocache /usr/bin/time -p go test ./...` | 0 | real 27.46s | 全部 package 通过 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage0-gocache /usr/bin/time -p go test -race ./...` | 0 | real 143.97s | 全部 package 通过，未报告 data race |
| `env GOCACHE=/tmp/modelfuzz-ng-stage0-gocache /usr/bin/time -p go vet ./...` | 0 | real 13.11s | 无诊断 |

普通测试的 CPU 时间为 user 35.14s、sys 11.59s；race 测试为 user
197.57s、sys 47.03s；vet 为 user 15.09s、sys 8.56s。`-race` 是实际完成
的全仓测试，未用普通测试替代。

## 4. 审计辅助命令中的环境问题

直接运行包依赖审计命令：

```text
go list -f '{{.ImportPath}} -> {{join .Imports ", "}}' ...
```

首次 exit code 为 1，首个真实错误是：

```text
open /home/test/.cache/go-build/...: read-only file system
```

判断：这是执行环境的用户级 Go cache 权限问题，不是代码、模块依赖或外部服务
问题，不阻塞后续重建。使用同一临时 cache 重新执行：

```text
env GOCACHE=/tmp/modelfuzz-ng-stage0-gocache go list -f \
  '{{.ImportPath}} -> {{join .Imports ", "}}' ...
```

exit code 为 0。结果确认：

- `internal/core` 只依赖标准库；
- `internal/runtime` 依赖 `internal/core`、`internal/sut`；
- `internal/plan` 依赖 `internal/core`；
- `internal/engine` 依赖 `core/model/oracle/plan/runtime`；
- `internal/corpus` 依赖 `model/plan`；
- `internal/minimize` 依赖 `core/engine/plan`；
- `internal/experiment` 横跨 `core/corpus/engine/metrics/model/mutation/plan`。

这也支持将未来 Assurance 保持为消费完成结果的叶层，而不是让上述稳定包反向
依赖它。

## 5. 当前模型与入口

模型 profile 不是全局常量，而由运行配置决定：

- `examples/config.json`：默认 `basic` profile，3 节点，
  `max_log_index=5`、`largest_term=5`，snapshot threshold 为 0；
- `examples/config-snapshot.json`：`storage-snapshot` profile，3 节点，
  `max_log_index=10`、`largest_term=10`，snapshot threshold 为 2、
  retain entries 为 0；
- 其他 tracked 示例包括 5 节点 snapshot、soak 和三个 mutant 配置。

模型文件是 `models/raft/raft.tla` 与
`models/raft/raft_storage_snapshot.tla`，对应多个 `.cfg` bounds。TLC
服务脚本 `tools/tlc-server/run.sh` 使用固定的 `tla2tools-1.8.0.jar`。
主要程序入口是 `cmd/modelfuzz-ng/main.go` 的 `runCLI`，现有子命令为
`run`、`replay`、`minimize`、`experiment`。本阶段没有修改或运行这些模型、
配置或服务。

## 6. 结束验收

四份文档写完后执行：

| 命令 | exit code | 结果 |
|---|---:|---|
| `git status --short` | 0 | `?? docs/semantic-assurance-rebuild/` |
| `git status --short --untracked-files=all` | 0 | 精确列出本目录下四个新 Markdown 文件 |
| `git diff --stat` | 0 | 无输出；原因是四个文件尚未跟踪，本阶段禁止 `git add` |
| `git diff --check` | 0 | 无输出 |
| `rg -n '[[:blank:]]+$' docs/semantic-assurance-rebuild/stage0` | 1 | 无匹配，即四个未跟踪文档也没有行尾空白 |

文件清单为：

```text
docs/semantic-assurance-rebuild/stage0/BASELINE_AUDIT.md
docs/semantic-assurance-rebuild/stage0/COMMAND_RESULTS.md
docs/semantic-assurance-rebuild/stage0/KERNEL_BOUNDARY.md
docs/semantic-assurance-rebuild/stage0/STAGE1_EXECUTION_RECORD_SPEC.md
```

没有其他文件、代码、配置、测试、TLA+、Java 或已有文档变更；未执行
`git add`、commit 或 push。`git diff --check` 不检查未跟踪文件，因此额外使用
只读 `rg` 检查四个文件的行尾空白。
