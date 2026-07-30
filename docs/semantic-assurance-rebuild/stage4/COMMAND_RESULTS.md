# Stage 4 命令结果

执行日期：2026-07-30

仓库：`/home/test/Desktop/modelfuzz-ng-semantic-assurance-rebuild-v1`

Go：`go1.26.4 linux/amd64`

## 1. 初始状态

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `75d4e51120b370acb880d003629f916da3f1a080` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1` |
| `git status --short --untracked-files=all` | 0 | 仅批准的 Stage 0—3、executionrecord、Facet 文件 |
| `git diff --check` | 0 | 无输出 |

没有已跟踪文件修改；没有 Stage 4 文件。未执行 `git add`、commit、push、reset、
clean、rebase、cherry-pick 或切换分支。

## 2. 前置回归

| 命令 | Exit | 关键结果与判断 |
|---|---:|---|
| `env GOCACHE=/tmp/modelfuzz-ng-stage4-gocache go test ./internal/assurance/executionrecord -count=1` | 0 | Stage 1 package 通过 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage4-gocache go test ./internal/assurance/facet/... -count=1` | 0 | Stage 3 packages 通过 |
| `env GOCACHE=/tmp/modelfuzz-ng-stage4-gocache go test ./... -count=1`（受限 sandbox） | 1 | 首个真实错误为现有 `httptest` 无权监听 `[::1]:0`，环境限制 |
| 同一命令允许本地回环监听重跑 | 0 | 相同代码状态全部 package 通过；未访问外部网络 |

## 3. Pilot

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `go test ./internal/assurance/facet/raft -run TestRealTraceFacetPilot -v -count=1` | 0 | 8 场景 ×3；24 candidate；约 1.6 秒 |
| 同一 Pilot `-count=3` | 0 | 三次完整 Pilot 通过；约 4.7 秒 |
| `go test -race ... -run TestRealTraceFacetPilot -count=1` | 0 | race 通过；测试执行约 10.3 秒 |

Pilot 观察到 Election 6 类、Replication 4 类、Snapshot 10 类。全部 state Facet 为
`evaluated`；Snapshot 为 12 次 `evaluated`、12 次 `not_applicable`；无
invalid/insufficient。24 candidate 的 Plan/Concrete action/Trace step 均合计 417。

## 4. 最终验证

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `go test ./internal/assurance/facet/... -count=1` | 0 | Core 与 Raft（含 Pilot）通过；约 3.8 秒 |
| `go test ./internal/assurance/facet/... -count=20` | 0 | 20 次全部通过；约 32.7 秒 |
| `go test ./internal/assurance/executionrecord -count=1` | 0 | Stage 1 回归通过；约 1.0 秒 |
| `go test ./... -count=1`（受限 sandbox） | 1 | `cmd/modelfuzz-ng` 首先因 `httptest` 无权监听 `[::1]:0` panic；`internal/llm`、`internal/model/tlc` 同类；环境限制 |
| 同一全仓 test（允许本地回环） | 0 | 全部 package 通过；约 15.5 秒 |
| `go test -race ./... -count=1`（允许本地回环） | 0 | 全部 package 通过；约 70.8 秒；race=0 |
| `go vet ./...` | 0 | 无输出；约 14.0 秒 |
| `gofmt -l internal/assurance/facet/raft` | 0 | 无输出 |
| `go list -f ... ./internal/assurance/facet/...` | 0 | 生产 Facet 直接依赖与 Stage 3 相同 |
| 禁止生产 import 的 `rg` | 1 | 无匹配；exit 1 表示未找到 |
| `git diff --check` | 0 | 无输出 |
| Stage 4/test 路径 whitespace `rg` | 1 | 无匹配；exit 1 表示没有行尾空白 |
| `git status --short --untracked-files=all` | 0 | 仅批准的 Stage 0—3、executionrecord、Facet，以及本阶段允许的新 test/docs |

生产依赖审计：

```text
internal/assurance/facet
  -> standard library, executionrecord, core, model

internal/assurance/facet/raft
  -> standard library, facet, core
```

Stage 4 只新增三个 `_test.go` 和四份文档；没有 testdata。没有修改任何已存在文件，
也没有新增生产 import。

未启动真实 TLC、未运行 replay、未写 Artifact、未执行 mutation candidate、未运行
长时间 fuzz campaign。
