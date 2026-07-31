# Stage 5 命令结果

## 1. 环境与初始状态

人工将 Stage 5 基线更新为当前 Stage 4 分支：

| 命令 | 关键结果 | Exit |
|---|---|---:|
| `git rev-parse HEAD` | `d281e2f2fdaba85a8e45d227f6342f93849eb5e0` | 0 |
| `git branch --show-current` | `agent/semantic-assurance-rebuild-v1-stage4` | 0 |
| `git status --short --untracked-files=all` | 无输出，工作区干净 | 0 |
| `git diff --check` | 无输出 | 0 |

## 2. 编码前回归

统一使用：

```text
GOCACHE=/tmp/modelfuzz-ng-stage5-gocache
```

| 命令 | 结果 | Exit | 约耗时 |
|---|---|---:|---:|
| `go test ./internal/assurance/executionrecord -count=1` | 通过 | 0 | 0.04s（首次 cache 构建另计） |
| `go test ./internal/assurance/facet/... -count=1` | 通过 | 0 | 2.09s |
| `go test ./... -count=1`（受限 sandbox） | `httptest` 无权监听 `[::1]:0` | 1 | 30s |
| 同一全仓命令（允许本地回环） | 全部通过 | 0 | 15.55s |

首次全仓失败的首个真实错误：

```text
httptest: failed to listen on a port:
listen tcp6 [::1]:0: socket: operation not permitted
```

判断：sandbox 环境权限，不是代码问题，不阻塞；相同代码状态允许回环后通过。

## 3. Stage 5 package

| 命令 | 结果 | Exit | 约耗时 |
|---|---|---:|---:|
| `go test ./internal/assurance/facetbreadth -count=1` | 通过 | 0 | 1.77s |
| `go test ./internal/assurance/facetbreadth -count=20` | 通过 | 0 | 62.85s |
| `go test -race ./internal/assurance/facetbreadth -count=1` | 通过，无 race | 0 | 18.07s |
| `go test ./internal/assurance/facetbreadth -cover -count=1` | 通过，86.7% statements | 0 | 3.98s |
| `go vet ./internal/assurance/facetbreadth` | 通过 | 0 | 约 30s（共享 cache 构建） |
| `go test ... -run TestRealTraceBreadthShadow -v -count=1` | 通过，20-key union | 0 | 0.52s |

真实 shadow union：

```text
Election=6
Replication=4
Snapshot=10
Total=20
```

固定 8 场景顺序的 decision 均为 `new_facet_class`。其他四种 reason 由 unit tests
覆盖。

## 4. Stage 1—4 回归

| 命令 | 结果 | Exit | 约耗时 |
|---|---|---:|---:|
| `go test ./internal/assurance/executionrecord -count=1` | 通过 | 0 | 0.12s |
| `go test ./internal/assurance/facet/... -count=20` | 通过 | 0 | 37.33s |
| `go test -race ./internal/assurance/facet/... -count=1` | 通过 | 0 | 13.00s |
| `go test ./internal/assurance/facet/raft -run TestRealTraceFacetPilot -count=1` | 通过 | 0 | 5.19s |

## 5. 全仓最终验证

| 命令 | 结果 | Exit | 约耗时 |
|---|---|---:|---:|
| `go test ./... -count=1`（受限 sandbox） | 仅既有 httptest 回环权限失败 | 1 | 约 6s |
| 同一命令（允许本地回环） | 全部通过 | 0 | 16.51s |
| `go test -race ./... -count=1`（受限 sandbox） | 仅既有 httptest 回环权限失败 | 1 | 约 60s |
| 同一 race 命令（允许本地回环） | 全部通过，无 race | 0 | 约 60s |
| `go vet ./...` | 通过 | 0 | 9.54s |

受限 race 的首个真实错误仍为：

```text
httptest: failed to listen on a port:
listen tcp6 [::1]:0: socket: operation not permitted
```

没有修改既有测试；允许本地回环后的相同代码状态通过。

## 6. 格式、依赖与边界

| 命令 | 结果 | Exit |
|---|---|---:|
| `gofmt -l internal/assurance/facetbreadth` | 无输出 | 0 |
| `git diff --check` | 无输出 | 0 |
| `rg -n '[[:blank:]]+$' internal/assurance/facetbreadth docs/.../stage5` | 无匹配 | 1（预期） |
| 禁止生产 import `rg` | 无匹配 | 1（预期） |
| 反向 import `facetbreadth` 的 `rg` | 无匹配 | 1（预期） |

首次直接执行 `go list` 因默认 `/home/test/.cache/go-build` 只读而退出 1；这属于环境
cache 路径问题。使用统一显式 GOCACHE 重跑：

```text
github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth
-> crypto/sha256, encoding/hex, encoding/json, fmt,
   internal/assurance/executionrecord, internal/assurance/facet,
   reflect, sort, sync
```

结果：Exit 0；无禁止生产依赖。

## 7. 文件与最终状态

生产文件 4 个，共 1,071 行；测试文件 6 个。最终路径审计只应显示：

```text
internal/assurance/facetbreadth/
docs/semantic-assurance-rebuild/stage5/
```

最终 `git status`、`git diff --stat`、`git diff --check` 和 whitespace 结果在报告
完成后再次执行；本阶段不执行 `git add`、commit 或 push。
