# Stage 3 命令结果

执行日期：2026-07-30

仓库：`/home/test/Desktop/modelfuzz-ng-semantic-assurance-rebuild-v1`

Go：`go1.26.4 linux/amd64`

## 1. 初始状态

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `75d4e51120b370acb880d003629f916da3f1a080` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1` |
| `git status --short --untracked-files=all` | 0 | 仅 Stage 0/1/2 文档及 `internal/assurance/executionrecord/` 为已批准未跟踪输入 |
| `git diff --check` | 0 | 无输出 |

开始前没有已跟踪文件修改，没有 Stage 3 文件。未执行 `git add`、commit、push、
reset、clean、rebase 或分支切换。

## 2. 前置回归

| 命令 | Exit | 关键结果与判断 |
|---|---:|---|
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./internal/assurance/executionrecord -count=1` | 0 | Stage 1 package 通过 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./... -count=1`（受限沙箱） | 1 | 首个真实错误：`httptest: failed to listen on a port: listen tcp6 [::1]:0: socket: operation not permitted`；环境限制 |
| 同一全仓命令（允许本地回环监听） | 0 | 全部 package 通过；确认不是代码问题 |

前置检查没有发现基线、Stage 1 或冻结语义冲突。

## 3. Stage 3 package 验证

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./internal/assurance/facet/... -count=1` | 0 | Core 与 Raft 通过 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./internal/assurance/facet/... -count=20` | 0 | Core 20 次、Raft 20 次全部通过，无非确定性失败 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test -race ./internal/assurance/facet/... -count=1` | 0 | Core 与 Raft race 通过 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./internal/assurance/executionrecord -count=1` | 0 | Stage 1 回归通过 |

Fixture 测试实际加载并执行 31/31 cases；验证 manifest bytes/SHA、静态
canonical key、静态 KeyDigest、first occurrence 和完整 class coverage。

## 4. 全仓验证

| 命令 | Exit | 关键结果与判断 |
|---|---:|---|
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./... -count=1`（受限沙箱） | 1 | `cmd/modelfuzz-ng` 首先因 `httptest` 无权监听 `[::1]:0` panic；`internal/llm`、`internal/model/tlc` 同类；环境问题 |
| 同一普通测试（允许本地回环监听） | 0 | 最终重跑全部 package 通过；`cmd/modelfuzz-ng` 约 10.4 秒 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test -race ./... -count=1`（允许本地回环监听） | 0 | 最终重跑全部 package 通过；最长 `cmd/modelfuzz-ng` 约 50.9 秒 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go vet ./...` | 0 | 无输出 |

受限沙箱失败与 Prompt 预告的本地回环权限情况一致。重跑只放开本机 test listener，
没有访问外部网络、没有启动真实 TLC 服务或运行 fuzz campaign。

## 5. 覆盖率、格式与依赖

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go test ./internal/assurance/facet/... -cover -count=1` | 0 | `facet` 80.7%；`facet/raft` 91.4% statements |
| `gofmt -l internal/assurance/facet` | 0 | 无输出 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage3-gocache go list -f '{{.ImportPath}} -> {{join .Imports ", "}}' ./internal/assurance/facet/...` | 0 | 仅标准库及允许的 executionrecord/core/model/facet 直接依赖 |
| 禁止生产 import 的 `rg` | 1 | 无匹配；exit 1 表示未找到 |
| `python3 -m json.tool` 检查四个 fixture JSON | 0 | 全部合法 JSON |
| `git diff --check` | 0 | 无输出 |

一次辅助 `go doc` 命令在受限环境尝试查询未缓存模块元数据时出现代理权限错误；该命令
不属于验收命令，不影响编译、测试、race、vet 或实现结论。

## 6. 最终检查

最终格式和状态检查在报告写入后执行：

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `gofmt -l internal/assurance/facet` | 0 | 无输出 |
| `git diff --check` | 0 | 无输出 |
| `rg -n '[[:blank:]]+$' internal/assurance/facet docs/semantic-assurance-rebuild/stage3` | 1 | 无匹配；exit 1 表示没有行尾空白 |
| `git status --short --untracked-files=all` | 0 | 仅已批准的 Stage 0—2、executionrecord，以及本阶段允许的 Facet/Stage 3 新文件；无已跟踪文件修改 |

最终确认：未启动真实 TLC、未运行 fuzz campaign、未生成大规模 Artifact，未修改任何
既有文件，未进入 Stage 4。
