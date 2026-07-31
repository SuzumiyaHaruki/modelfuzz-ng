# Stage 7 Command Results

日期：2026-07-30—31（Asia/Shanghai）

## 1. Initial state

| 命令 | Exit | 关键结果 |
|---|---:|---|
| `git rev-parse HEAD` | 0 | `d281e2f2fdaba85a8e45d227f6342f93849eb5e0` |
| `git branch --show-current` | 0 | `agent/semantic-assurance-rebuild-v1-stage4` |
| `git status --short --untracked-files=all` | 0 | 仅批准的 Stage 5、Stage 6 未跟踪文件 |
| `git diff --check` | 0 | 无输出 |

没有执行 switch、reset、clean、add、commit、push、rebase 或 cherry-pick。

## 2. Pre-regression

统一 cache：

```text
GOCACHE=/tmp/modelfuzz-ng-stage7-gocache
```

| 命令 | Exit | 结果 |
|---|---:|---|
| `go test ./internal/assurance/executionrecord -count=1` | 0 | 通过 |
| `go test ./internal/assurance/facet/... -count=1` | 0 | 通过 |
| `go test ./internal/assurance/facetbreadth -count=1` | 0 | 通过 |
| sandbox `go test ./... -count=1` | 1 | 首个真实错误：既有 `httptest` 无权监听 `[::1]:0` |
| 允许本机 loopback 后同状态重跑 | 0 | 全仓通过 |

Sandbox失败属于环境权限，不是代码问题；没有修改测试。

## 3. Historical and held-out audit

| 检查 | Exit | 结果 |
|---|---:|---|
| sibling historical `rg`/`sed` | 0 | 验证10个 seeds和正式数值配置 |
| held-out derivation一次性 Python | 0 | 20个 literal int64 |
| held-out overlap/appearance `rg` | 1 | 无匹配，表示未使用且不重叠 |

Historical initial逐槽规则未进入 tracked report/config，预注册为
`HISTORICAL_CONFIGURATION_REPLICATION_ONLY`；不读取旧实现补齐。

## 4. Frozen hashes

在 Stage 7 strict TLC formal run前填写；计算后不修改 preregistration。

| 对象 | SHA-256 |
|---|---|
| `STAGE7_PREREGISTRATION.md` | `62a386a2017e4f5bf7e8164e0478b73d7a2506914db9ffeb1701a4be1ffdbf9c` |
| `results/heldout-seeds.json` | `00da18df2172eada4938315b3db2e1abf3b7413ba6a58a1a6c03d2d5cc4be6f6` |

## 5. Implementation and formal execution

新增test-only harness：

- `stage7_helpers_test.go`；
- `stage7_heldout_test.go`；
- `stage7_replay_minimize_test.go`；
- `stage7_formal_evaluation_test.go`。

没有新增production Go文件，没有修改已有文件。

### 5.1 Quick harness

| 命令 | Exit | 结果 |
|---|---:|---|
| `go test ... -run 'TestStage7' -count=1` | 0 | 通过；formal gated tests skip |
| `go test ... -run 'TestStage7' -count=20` | 0 | 49.329s，确定性通过 |
| `go test -race ... -run 'TestStage7' -count=1` | 0 | 13.892s，无race |

### 5.2 Formal run过程

所有尝试都在同一冻结preregistration/seed-list SHA下运行；没有改变实验参数。

| 尝试 | Exit | 首个真实结果与处理 |
|---|---:|---|
| formal，默认Go timeout | 1 | 600.694s被默认10m测试上限终止；非断言/TLC失败，改用`-timeout 60m` |
| formal，60m | 1 | 1008.689s被系统`SIGKILL`；test harness跨seed保留全部Trace/Model State导致内存增长；改为pair检查后只保留final shortest/failure execution |
| 内存修复后formal | 1 | historical和held-out完成；mutant initial Plan recording错误地提前启用`invert`，在`initial/5` mapping failure |
| neutral initial修复后formal | 1 | historical和held-out完成；mutant neutral reseed recording仍提前启用`invert`，在`reseed/7` mapping failure |
| 所有Plan recording统一正常fault policy后formal | 0 | 1313.349s；historical=10、closed=20、neutral=20、mutant=10、replay=46、minimize=2 |

前两项是运行上限和test-only内存保留问题；后两项是mutant生效时机的test harness
错误。最终规则为：initial/reseed Plan使用正常fault policy确定记录，`invert`仅在正式
candidate execution生效。Seed、Plan派生、budget、mode、Facet或production均未改变。

补充预注册repeatability：

| 命令 | Exit | 结果 |
|---|---:|---|
| `TestStage7ClosedTreeAndMutantRepeatabilityStrictTLC` | 0 | 78.071s；closed和mutant两个mode均完全重复 |

## 6. Strict TLC

启动：

```text
tools/tlc-server/run.sh
  --model models/raft/raft_storage_snapshot.tla
  --config models/raft/raft-storage-snapshot-10.cfg
  --port 18773
```

正式通过实例：

| 项 | 值 |
|---|---|
| PID | `194656` |
| Java | OpenJDK 17.0.20 |
| TLA+ Tools | 1.8.0 |
| JAR SHA-256 | `cc4803dce2a8ffaf0f5920a9dc39df4b5ee34ab4cb53fb58ac557277a7e516b3` |
| strict/profile | `true` / `storage-snapshot` |
| Servers | `{1,2,3}` |
| MaxValue/Nil | `5` / `0` |
| MaxLogIndex/LargestTerm | `10` / `10` |
| requests/succeeded/failed | `10648 / 10648 / 0` |
| model events | `362972` |
| action cache | `2773` entries，0 eviction |
| shutdown | 仅本阶段进程收到Ctrl-C，exit 130 |

没有连接外部TLC或外部网络。

## 7. Analysis, replay and minimize

| 命令/检查 | Exit | 结果 |
|---|---:|---|
| `python3 stage7/analyze_results.py stage7/results` | 0 | 10,000 paired bootstrap；写aggregate JSON和paired CSV |
| representative replay | 0 | 46 slots、33 distinct records、0 mismatch |
| mutant minimize | 0 | 两mode均40→18 actions，136 attempts，one-minimal，signature稳定 |
| frozen prereg SHA复核 | 0 | 仍为`62a386...bf9c` |
| held-out seed-list SHA复核 | 0 | 仍为`00da18...e6f6` |

Results：

- 127个JSON和1个CSV；
- 总文件数128；
- 总bytes `29,681,283`；
- 不包含完整Trace、Model State/Event、Observation或Finding payload；
- 仅两份允许的紧凑minimized Plan。

## 8. Regression

| 命令 | Exit | 结果 |
|---|---:|---|
| `go test ./internal/assurance/executionrecord -count=1` | 0 | 通过 |
| `go test ./internal/assurance/facet/... -count=1` | 0 | 通过 |
| `go test ./internal/assurance/facetbreadth -count=20` | 0 | 36.574s，通过 |
| sandbox `go test ./... -count=1` | 1 | 仅既有`httptest`无法监听`[::1]:0` |
| loopback `go test ./... -count=1` | 0 | 全仓通过 |
| loopback `go test -race ./... -count=1` | 0 | 全仓通过，无race |
| `go vet ./...` | 0 | 无输出 |

## 9. Format, dependency and final state

| 检查 | Exit | 结果 |
|---|---:|---|
| `gofmt -l internal/assurance/facet/raft` | 0 | 无输出 |
| `git diff --check` | 0 | 无输出 |
| Stage 7 whitespace `rg` | 1 | 无匹配，表示通过 |
| production `go list` | 0 | Facet/Facet Breadth依赖保持冻结方向 |
| forbidden production import `rg` | 1 | 无匹配 |
| reverse production import `rg` | 1 | 无匹配；命中仅存在于允许的test-only harness |
| results JSON parse/schema/payload check | 0 | 127/127 JSON通过 |

最终：

- HEAD仍为`d281e2f2fdaba85a8e45d227f6342f93849eb5e0`；
- branch仍为`agent/semantic-assurance-rebuild-v1-stage4`；
- `git diff --name-only`为空，tracked worktree没有修改；
- status只包含批准的Stage 5、Stage 6和本轮Stage 7未跟踪文件；
- 没有git add、commit、push、switch、reset、clean、rebase或cherry-pick。
