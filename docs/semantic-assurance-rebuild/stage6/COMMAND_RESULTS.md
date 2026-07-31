# Stage 6 Command Results

日期：2026-07-30（Asia/Shanghai）

## 1. 初始状态

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `d281e2f2fdaba85a8e45d227f6342f93849eb5e0` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1-stage4` |
| `git status --short --untracked-files=all` | 0 | 仅 Stage 5 批准的 4 production、6 test、2 report |
| `git diff --check` | 0 | 无输出 |
| `go version` | 0 | `go1.26.4 linux/amd64` |

未执行 switch、reset、clean、add、commit、push、rebase 或 cherry-pick。

## 2. 前置回归

最初并行启动多个使用同一空 GOCACHE 的测试造成 cache/build 争用，人工中断三个尚未
产生测试结果的进程（exit 130），随后串行/明确复跑；这不是代码失败。

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `go test ./internal/assurance/executionrecord -count=1` | 0 | package 0.091s |
| `go test ./internal/assurance/facet/... -count=1` | 0 | Facet 0.018s；Raft 2.212s |
| `go test ./internal/assurance/facetbreadth -count=1` | 0 | 2.483s |
| sandbox `go test ./... -count=1` | 1 | 首错为既有 `httptest` 无权监听 `[::1]:0` |
| 允许本地回环后的同状态重跑 | 0 | 全仓通过 |

没有为前置回归修改既有代码或测试。

## 3. Mutator 审计

使用 `sed`/`rg` 阅读：

- `internal/mutation/mutation.go`
- `internal/mutation/random.go`
- `internal/experiment/runner.go`
- `internal/corpus/corpus.go`

命令均 exit 0。确认 `mutation.Random` 使用显式 `Request.Seed` 的局部 RNG，复制 parent
Plan，不读取 coverage、admission、wall-clock 或全局随机源。

## 4. Stage 6 快速测试

| 命令 | Exit | 实际耗时 |
|---|---:|---:|
| `go test ./internal/assurance/facet/raft -run 'TestActiveAB' -count=1` | 0 | real 9.94s |
| `go test ./internal/assurance/facet/raft -run 'TestActiveAB' -count=20` | 0 | real 114.41s |
| `go test -race ./internal/assurance/facet/raft -run 'TestActiveAB' -count=1` | 0 | real 36.53s |

覆盖 initial population deep-copy、production mutation determinism、固定 budget/energy、
两种 active guidance、overlap facts、重复性和 canonical test result。

## 5. strict TLC 服务

| 操作 | Exit | 关键结果 |
|---|---:|---|
| 首次 `tools/tlc-server/run.sh ... --port 18761` | 7 | 新目录无 jar，脚本 `curl` 无法连接；未联网 |
| 本地缓存 `sha256sum` | 0 | `cc4803dce2a8ffaf0f5920a9dc39df4b5ee34ab4cb53fb58ac557277a7e516b3`，与脚本冻结值相同 |
| `tools/tlc-server/build.sh` | 0 | 使用本地匹配缓存构建 ignored server jar |
| sandbox 内启动 | 1 | `SocketException: Operation not permitted` |
| 允许本地回环后启动 | running | PID 对应独占 test session，端口 18761 |
| `curl .../health` | 0 | strict v1，storage-snapshot，Servers 1/2/3，5/0，10/10 |
| `java -version` | 0 | Temurin OpenJDK 17.0.20+8 |
| 最终 `curl .../metrics` | 0 | 780 requests，780 success，0 failed，15,571 events |
| 关闭本阶段服务 | 130 | 预期 SIGINT；仅关闭本阶段启动进程 |

## 6. 正式 A/B

命令：

```text
MODELFUZZ_STAGE6_TLC_URL=http://127.0.0.1:18761 \
env GOCACHE=/tmp/modelfuzz-ng-stage6-gocache \
go test ./internal/assurance/facet/raft \
  -run TestActiveFacetABSmokeStrictTLC -v -count=1
```

| Run | Exit | 耗时 | 说明 |
|---|---:|---:|---|
| 首次机制诊断 | 1 | 8.71s | baseline seed 6601 在 40/48 自然 queue exhaustion；测试误将 budget 上限断言成必须执行满 |
| 修正断言后 | 0 | 27.83s | 机制 GO，完整比较与 6601 重复通过 |
| compact-log 同语义重跑 | 0 | 27.20s | 完整 canonical metrics 可审计；结果相同 |

断言修正没有改变预注册参数。`candidate budget=48` 是两 mode 相同上限；协议已明确
queue exhaustion 时不得补种。正式主比较实际 258 candidates，性能方向
`SIGNAL_NEGATIVE`。

## 7. Stage 1—5 与最终全仓回归

| 命令 | Exit | 实际耗时/结果 |
|---|---:|---|
| `go test ./internal/assurance/executionrecord -count=1` | 0 | real 2.44s，package 0.059s |
| `go test ./internal/assurance/facet/... -count=1` | 0 | real 11.92s |
| `go test ./internal/assurance/facetbreadth -count=20` | 0 | real 36.97s |
| `go test -race ./internal/assurance/facetbreadth -count=1` | 0 | real 23.68s |
| sandbox `go test ./... -count=1` | 1 | real 14.45s，仅既有 `[::1]:0` 权限失败 |
| 允许本地回环 `go test ./... -count=1` | 0 | real 20.10s，全仓通过 |
| sandbox `go test -race ./... -count=1` | 1 | real 67.43s，仅既有 `[::1]:0` 权限失败 |
| 允许本地回环 `go test -race ./... -count=1` | 0 | real 84.35s，全仓 race 通过 |
| `go vet ./...` | 0 | real 13.80s |

Strict gated test在未设置环境变量的普通全仓测试中正常 Skip。

## 8. 格式、依赖和工作区

| 命令 | Exit | 结果 |
|---|---:|---|
| `gofmt -l internal/assurance/facet/raft` | 0 | 无输出 |
| `git diff --check` | 0 | 无输出 |
| whitespace `rg` | 1 | 无匹配，表示通过 |
| 第一次无 `GOCACHE` 的 `go list` | 子命令失败 | default cache 只读；没有代码问题 |
| 设置 Stage 6 GOCACHE 后 `go list ...` | 0 | 依赖列出成功 |
| production reverse-import `rg` | 1 | 无匹配 |

生产依赖保持 Stage 3/5 原状：

- `facet` 依赖 `executionrecord/core/model`；
- Raft Facet 依赖 `facet/core`；
- `facetbreadth` 只依赖 `facet/executionrecord`；
- Stage 6 对 Runtime/Engine/Experiment/Corpus/Adapter/TLC 的依赖只存在于 `_test.go`；
- 没有 production package 反向 import `facetbreadth`。

最终允许的新 Stage 6 文件应为 3 个 `_test.go` 和 4 份文档；Stage 5 未跟踪批准输入
保持原样。没有 tracked 修改，ignored TLC cache/build 不进入 status。
