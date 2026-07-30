# Stage 2 命令结果

## 1. 初始状态

工作目录：

`/home/test/Desktop/modelfuzz-ng-semantic-assurance-rebuild-v1`

| 命令 | exit code | 关键结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `75d4e51120b370acb880d003629f916da3f1a080` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1` |
| `git status --short --untracked-files=all` | 0 | 只有已批准的 Stage 0 四份文档、Stage 1 两份文档和 `internal/assurance/executionrecord/` 七个文件 |
| `git diff --check` | 0 | 无输出 |

初始状态没有其他 tracked 修改或未批准 untracked 文件。Stage 0/1 文件均未删除、
移动、覆盖或修改。

## 2. 代码验证

统一缓存：

`GOCACHE=/tmp/modelfuzz-ng-stage2-gocache`

| 命令 | exit code | 耗时/关键结果 |
|---|---:|---|
| `go test ./internal/assurance/executionrecord -count=1` | 0 | package 输出 `ok`；test execution `0.035s`，冷编译墙钟约 51s |
| `go test ./... -count=1`（受限 sandbox） | 1 | 首个真实错误：`httptest: failed to listen on a port: listen tcp6 [::1]:0: socket: operation not permitted` |
| `go test ./... -count=1`（允许本地回环监听） | 0 | 全部 package 通过；墙钟 `12.8s` |
| `go test -race ./... -count=1`（允许本地回环监听） | 0 | 全部 package 通过，race report 为 0；墙钟约 `122s` |
| `go vet ./...` | 0 | 无输出；墙钟 `13.3s` |

第一次全仓测试失败属于执行环境禁止 `httptest.NewServer` 监听本地回环端口，
不是代码、TLC 服务或外部依赖失败。相同 HEAD 和工作树在只放开本地测试监听后
完整通过。测试没有访问外部网络，没有启动真实 TLC 服务，也没有运行 fuzz
campaign。

## 3. 格式与范围验证

| 命令 | exit code | 关键结果 |
|---|---:|---|
| `gofmt -l internal/assurance/executionrecord` | 0 | 无输出 |
| `git diff --check` | 0 | 无输出；注意 Stage 2 文档尚未 tracked |
| `rg -n '[[:blank:]]+$' docs/semantic-assurance-rebuild/stage2`（首次） | 0 | 发现 `RAFT_FACET_CATALOG_V1.md` 一处行尾空格 |
| 同一 `rg`（修正后） | 1 | 无匹配；exit 1 表示没有行尾空白 |

首次空白检查发现的问题只位于本阶段新文档，已删除该行尾空格；未修改代码或既有
文档。

## 4. 最终范围

Stage 2 新增且仅新增：

1. `FACET_V1_SEMANTIC_CONTRACT.md`
2. `FACET_EVIDENCE_MATRIX.md`
3. `RAFT_FACET_CATALOG_V1.md`
4. `STAGE3_OFFLINE_FACET_IMPLEMENTATION_SPEC.md`
5. `COMMAND_RESULTS.md`

最终检查实际结果：

| 命令 | exit code | 结果 |
|---|---:|---|
| `git status --short --untracked-files=all` | 0 | 仅列出已批准 Stage 0/1 输入和上述五个 Stage 2 文档 |
| `git diff --stat` | 0 | 无输出；所有批准内容仍为 untracked，未暂存 |
| `git diff --check` | 0 | 无输出 |
| `rg -n '[[:blank:]]+$' docs/semantic-assurance-rebuild/stage2` | 1 | 无匹配，符合预期 |

最终状态：除已批准 Stage 0/1 输入外，新增内容只在
`docs/semantic-assurance-rebuild/stage2/`；没有代码、配置、测试、TLA+ 或 Java
变更。
