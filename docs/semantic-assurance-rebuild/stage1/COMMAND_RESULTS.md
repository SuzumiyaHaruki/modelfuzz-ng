# Stage 1 命令结果

## 1. 初始状态

工作目录：

```text
/home/test/Desktop/modelfuzz-ng-semantic-assurance-rebuild-v1
```

| 命令 | exit code | 结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `75d4e51120b370acb880d003629f916da3f1a080` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1` |
| `git status --short --untracked-files=all` | 0 | 仅四份 Stage 0 文档未跟踪 |
| `git diff --check` | 0 | 无输出 |

四份 Stage 0 文档已逐段完整读取，未修改。

## 2. 实现前基线

Go 命令统一使用：

```text
GOCACHE=/tmp/modelfuzz-ng-stage1-gocache
```

首次在受限沙箱运行：

```text
go test ./... -count=1
```

exit code 为 1，real 88.16s。首个真实错误：

```text
httptest: failed to listen on a port:
listen tcp6 [::1]:0: socket: operation not permitted
```

同类错误出现在既有 `cmd/modelfuzz-ng`、`internal/llm` 和
`internal/model/tlc` 的 `httptest.NewServer`。判断为沙箱禁止本机回环监听，
不是代码、依赖或外部服务问题。允许本机测试端口后使用同一源码重跑：

| 命令 | exit code | 耗时 | 结果 |
|---|---:|---:|---|
| `go test ./... -count=1` | 0 | real 12.67s | 全仓通过 |
| `go vet ./...` | 0 | real 13.71s | 无诊断 |

实现前全仓 race 首次 exit code 为 1，real 138.31s；唯一失败为既有
`TestFeedbackRunnerResubmitsPendingMutationAfterResume`。失败状态显示
`OnRunComplete` 取消后，下一 initial candidate 已进入 `InFlight`，mutation
尚未进入 `PendingMutations`。这是测试中取消动作与两个 worker 调度顺序的现有
波动。只读检查源码后执行：

```text
go test -race ./internal/experiment \
  -run '^TestFeedbackRunnerResubmitsPendingMutationAfterResume$' -count=20
```

exit code 为 0，real 2.61s。普通全仓基线、受影响测试重复 race 和后续最终
全仓 race 均通过，因此该现象被记录为实现前既有时序 flake，不归因于 Stage 1。

## 3. Package 验证

最终 package 验证：

| 命令 | exit code | 结果 |
|---|---:|---|
| `go test ./internal/assurance/executionrecord -count=1` | 0 | 通过 |
| `go test ./internal/assurance/executionrecord -count=20` | 0 | 通过；无确定性波动 |
| `go test -race ./internal/assurance/executionrecord -count=1` | 0 | 通过；无 race |
| `go vet ./internal/assurance/executionrecord` | 0 | 无代码诊断 |
| `go test ./internal/assurance/executionrecord -cover -count=1` | 0 | 最终 83.7% statements |

测试过程中出现过两类即时开发错误：

1. 初始 production draft 的 Oracle helper 类型与 `[]oracle.Finding` 不匹配；
2. 新增 invalid UTF-8 vector 时一个 `append` 缺失右括号。

两者均为新 package 内的编译错误，首个错误明确、未涉及 Frozen Kernel；已在
继续验证前最小修正。随后 package 普通、20 次和 race 全部通过。

## 4. 实现后全仓验证

现有测试使用 `httptest` 本机回环端口，因此全仓 test/race 在允许该本机测试
能力的环境执行；没有访问外部网络目标，也没有启动真实 TLC 服务。

| 命令 | exit code | 耗时 | 结果 |
|---|---:|---:|---|
| `go test ./... -count=1` | 0 | real 12.84s | 全仓通过 |
| `go test -race ./... -count=1` | 0 | real 50.86s | 全仓通过，无 race |
| `go vet ./...` | 0 | real 0.93s | 无诊断 |
| `gofmt -l internal/assurance/executionrecord` | 0 | 无输出 |

一次非验收的 `go doc` 辅助审计触发默认 `goproxy.cn` lookup，被沙箱以
`socket: operation not permitted` 拒绝。它不影响构建测试；使用
`GOPROXY=off` 后从本地源码列出了新 package API。没有为此修改依赖或访问外部
目标。

## 5. 依赖与范围审计

`go list` 确认新 package 的仓库内直接依赖为：

```text
internal/core
internal/engine
internal/experiment
internal/minimize
internal/oracle
```

`rg` 没有发现任何既有 package import `internal/assurance/executionrecord`。
没有 package cycle 或反向依赖。

结束时再次执行：

```text
git diff --check
gofmt -l internal/assurance/executionrecord
rg -n '[[:blank:]]+$' \
  internal/assurance/executionrecord \
  docs/semantic-assurance-rebuild/stage1
git status --short --untracked-files=all
```

结果：

- `gofmt -l internal/assurance/executionrecord`：exit 0，无输出；
- `git diff --check`：exit 0，无输出；
- 行尾空白 `rg`：无匹配；单独运行时 exit 1，语义为“没有匹配”；
- `git status --short --untracked-files=all`：仅列出四份原 Stage 0 文档、
  Stage 1 两份报告和 `executionrecord` 的 4 个生产/3 个测试文件；
- 未出现任何已修改的 tracked 文件。

由于全部允许文件均尚未跟踪，`git diff --check` 不覆盖它们；额外 `rg` 已覆盖
所有 Stage 1 新文件。没有执行 `git add`、commit 或 push。
