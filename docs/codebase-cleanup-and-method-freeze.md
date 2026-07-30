# 代码整理与方法冻结报告

日期：2026-07-29。范围仅限本地仓库和进程内 etcd-raft 研究环境。本轮 LLM 调用为 0。

## 1. 本轮动机

前七轮逐步加入 v2、Facet、Goal/Waypoint、Branch、Evidence 和 focused mutation，研究证据已经足够，但代码、CLI、manifest 和文档容易让读者误把所有原型都当成当前方法。本轮目标是建立可审计边界、删除强证据死代码并保持全部 accepted artifact 可复现，不增加新功能，也不重写算法。

## 2. 当前方法主线

冻结主线是：

1. Facet 表示协议语义探索广度；
2. Waypoint Frontier 以 staged Distance、prefix preservation 和 replay 推进特定 Goal；
3. Protocol-Aware Local Mutation 在 Waypoint 之间使用当前 Observation 组织有限局部动作。

这三层分别回答“覆盖了哪些语义区域”“离目标还有多远”“下一小段可执行动作如何组织”，不是把所有信息拼成一个总分。

## 3. 冻结实验模块

Planned/Realized Branch、Partial Evidence、Commitment、Diversity/Evidence Frontier、Stage Budgeting、micro-progress 和 C=2/Failure-to-Form 分析冻结为实验或诊断能力。它们默认不改变普通 fuzz 和 Standard Frontier 排序，不再扩展新 schema 或新策略。

## 4. 整理前模块结构

修改前有 155 个 Go 文件、43,511 行；`internal/goalsearch` 约 8,500 行，既含主线 Goal/Standard Frontier，也含三轮 Branch/Evidence 原型；`cmd/modelfuzz-ng/goal_search.go` 同时承担 CLI、循环、兼容 artifact 和报告。完整清单见本文附录 A。

## 5. 整理后模块结构

- 协议无关执行核心仍在 core/plan/runtime/engine/trace；
- Raft 执行适配仍在 adapters/etcdraft；
- Facet 和 Raft model 在 model/raft；
- Goal/Waypoint/Standard Frontier 在 goalsearch 的 schema/evaluator/frontier/mutation；
- 通用 Advisor 在 protocolmutation；
- Raft focused 插件在 protocolmutation/raft；
- Branch/Evidence 原地保留，但文件顶部明确标为 frozen experimental/diagnostic；
- 文档和 manifest 通过统一索引区分主线、兼容、accepted 和 excluded。

未为目录整齐而移动类型或修改 import。

## 6. 删除原则

只有同时没有直接/间接引用、CLI/注册、schema、artifact、测试、文档和接口作用的私有代码才删除。“旧”“prototype”“当前默认不调用”都不是充分理由。审计证据见根目录 `cleanup-candidates.json`。

## 7. 删除内容

实际删除：

- 私有 `hasChange` 辅助函数 9 行：全仓只有声明；
- 默认 root usage 中 1 行冻结 C=2 诊断命令展示。

后者只是 hide-only：`runCLI` 的显式命令 switch、实现、schema 和历史读取能力全部保留。没有删除文件、manifest、测试、schema、ID 或 artifact reader。

## 8. 合并内容

实际合并为 0。CSV writer、JSON reader 和覆盖序列化表面相似，但错误语义、原子替换和稳定顺序不同；在缺少字节级 golden 前不做推测性合并。

## 9. 移动内容

实际移动为 0。Branch/Evidence 子包化和巨型文件拆分会制造大量无语义 diff，并扩大当前未提交研究成果的审计面，因此延期。

## 10. 保留但 deprecated 内容

`raft-coverage-v1`、`raft-coverage-v2-prototype`、旧 Frontier 消融 mode、`branch-budget-allocation` alias、历史 checkpoint/Trace/failure reader 都保留。这里的 deprecated 表示不再扩展，不表示可以改变持久化含义。

## 11. 保留实验模块及原因

Branch/Evidence 生产代码约 3,166 行，仍被第五、第六轮 manifest、artifact、负结果、record-only、CSV 和测试引用。删除会造成“只保留成功方法”的研究偏差，也会使 accepted artifact 无法重算，因此决定为 keep-experimental。

## 12. Artifact 兼容性

未改变文件名、JSON tag、字段缺省、顺序生成逻辑、stable key、checkpoint、Trace、failure signature、replay 或 ddmin reader。第七轮 M2/M3 既有正式 artifact 再审计中，38 个实际保存的 plan/trace/goal-progress 文件逐字节比较 mismatch 0；正式报告原有 M2/M3 总比较也是 mismatch 0。

## 13. Schema 兼容性

以下版本未变：

- Trace/checkpoint schema 1；
- `raft-coverage-v1`；
- `raft-coverage-v2-prototype`；
- `raft-coverage-facets-v1-prototype`；
- `raft-behavior-goals-v1-prototype`；
- `raft-behavior-branches-v1-prototype`；
- `raft-branch-evidence-v1-prototype`；
- `raft-branch-formation-failure-v1-prototype`；
- `mutation-advisor-v1`；
- `raft-goal-benchmark-v1`。

v1/v2/Facet/Goal/Branch/Evidence/Advisor/failure signature 的精确或确定性测试全部保留。

## 14. CLI 兼容性

所有子命令和 flag 仍可解析；只从默认 root usage 隐藏冻结的 `c2-differential-analysis`。显式输入该命令仍进入原实现。默认 `goal-search -h` 在用户显式请求完整选项时仍显示兼容 flags。没有删除正式复现命令。

## 15. go.mod 变化

`go.mod` 和 `go.sum` 均无变化。Go 版本仍为 1.26，toolchain 仍为 1.26.4，本地 `replace go.etcd.io/raft/v3 => ../raft` 保留，依赖未升级。

`go mod tidy -diff` 的审计尝试需要尚未缓存的上游 raft 测试依赖，并试图写只读 module cache；为遵守不联网、不改依赖的边界，没有强行 tidy。`go list -mod=readonly`、构建、测试和 vet 足以确认当前生产依赖闭包。

## 16. 默认路径是否简化

默认 root usage 不再把第六轮 C=2 诊断命令列为主线；README 首屏链接到冻结索引。算法执行路径没有改变：

- 普通 experiment 不启用 Branch/Evidence 或 focused；
- `goal-search` 默认仍为 Standard Waypoint Frontier；
- focused 仍需 `-mutation-advisor=raft-focused`；
- Diversity/Evidence mode 仍需显式选择。

为了保持历史 artifact，goal-search 即使实验模式关闭也会写 catalog/settings/空或诊断性文件；这些写入不进入 Standard Frontier 排序。直接删除这些文件会破坏 CLI artifact 契约，因此保留并在这里明确。

## 17. Branch/Evidence 隔离方式

采用三层隔离：

1. 文件顶部标为 frozen experimental/diagnostic；
2. 默认 mode/flag 不选择实验 Frontier 或 Stage allocator；
3. README 和统一索引不把其列入当前主线。

没有子包移动。record-only 与关闭的正式 M2/M3 计划、轨迹、Goal 和 Facet 路径保持一致。

## 18. Advisor 协议边界

通用 `protocolmutation` 只定义 Request、Candidate、Decision、Summary 和合法动作检查，只依赖 core/plan。Raft 插件根据消息类型、term、quorum、Snapshot boundary 和 restart 状态产生建议。goalsearch 只调用接口，CLI 负责选择具体插件。

## 19. Raft 专用代码泄漏审计

通用 Advisor 包没有 Raft import；core/runtime/engine 没有新增 Raft 判断；本轮对这些核心包改动为 0。Raft focused 判断全部留在 `internal/protocolmutation/raft`。Goal evaluator 本身仍是 Raft 语义实现，这是当前首协议的有意精确性，不伪装成通用协议模型。

## 20. 整理前后 SLOC

Go 文件保持 155；总行从 43,511 到 43,525，非测试从 30,598 到 30,612，测试保持 12,913。新增 24 行边界注释，删除 10 行代码/帮助展示，净增 14。二进制前后均为 17,460,096 字节。详细统计见本文附录 D。

## 21. 测试结果

修改前 `go test ./...` 通过；沙箱内第一次运行因 `httptest` 无权创建回环监听失败，获准在本地非网络目标环境重跑后全部通过。修改后的针对性回归和最终 `go test ./...` 全部通过。

## 22. Race 结果

修改前 `go test -race ./...` 全部通过。最终 race 也全部通过，没有数据竞争报告。

## 23. Vet 结果

修改前和最终 `go vet ./...` 均通过。Go 仅提示 module stat cache 位于只读目录，未产生 vet 诊断；GOCACHE 已显式放入 `/tmp`。

## 24. Replay 结果

整理后新建三个 replay 输出：

| 场景 | 状态 | 匹配步数 |
|---|---|---:|
| 正常 Goal A trace | completed | 21 |
| Snapshot mutant 最小失败 trace | completed | 15 |
| Restart mutant 最小失败 trace | completed | 4 |

输出位于 `/tmp/modelfuzz-ng-cleanup-replay-{normal,snapshot-mutant,restart-mutant}`。

## 25. Goal 回归

- legacy Standard Frontier 单测在固定 seed 下两个 Goal 均到达全部 Waypoint，online/offline mismatch 0，prefix replay 全成功；
- focused Goal A smoke：13 candidates、225 actions、目标到达、12/12 prefix replay；
- focused Goal B smoke：6 candidates、62 actions、目标到达、5/5 prefix replay；
- 两次 focused smoke 均为 LLM calls 0、online/offline mismatch 0、Branch Evidence mode off。

## 26. Coverage/Facet 回归

`coverage-compare` fixture 通过，输入 artifact 未被修改；`coverage-factorize` fixture 通过，Facet/Interaction 确定且不原地写输入；v1 serialization、v2 deterministic projection、Facet symmetry/determinism 测试均通过。

## 27. Record-only 一致性

三层证据一致：

1. 单元测试证明 Advisor record-only 不改变 weak candidate；
2. 第七轮正式 M2/M3 的 Branch/Evidence record-only 总比较 mismatch 0；
3. 本轮重新读取正式目录，对可保存的 38 个 plan/trace/goal-progress 文件逐字节比较，mismatch 0。

因此 record-only 记录不会读取未来信息或改变搜索；它只增加诊断 artifact。

## 28. 删除决策表

逐项决策见本文附录 C，机器可读证据见仓库根目录 `cleanup-candidates.json`。报告明确区分 delete、hide-only、keep、keep-deprecated、keep-experimental 和因证据不足延期。

## 29. 仍然较大的模块

`goal_search.go`、`evaluator.go`、`branch.go`、`mutation.go` 和 `goal_benchmark.go` 仍较大。它们不是本轮功能缺陷；没有 artifact byte-golden 前，拆分收益不足以覆盖审计风险。

## 30. 尚未解决的技术债务

- 主线与冻结实验类型仍在同一个 goalsearch package；
- goal-search CLI、循环和 artifact wiring 仍集中；
- CSV writer 尚未统一；
- 默认 goal-search 为兼容旧行为仍是 advisor off，而论文主线 focused 需要显式 flag；
- 冻结实验关闭时仍写兼容 catalog/summary；
- 本地 sibling raft fork 和 TLC server 仍是复现环境要求；
- accepted `/tmp` artifact 还需发布时打包为版本化只读集合。

这些项目都已记录，不在本轮用高风险重写解决。

## 31. 对第二协议迁移的影响

可直接复用：

- Action/Plan/Observation/Trace/Runtime/Engine；
- Artifact/checkpoint/replay/ddmin；
- protocolmutation Advisor 接口和 summary；
- Frontier 的 replayable-prefix 思路。

必须由第二协议重新实现：

- Adapter、Observation semantic fields、Oracle、Mapper；
- Facet；
- Goal/Waypoint/Distance；
- protocol-aware Advisor。

不应迁移 Branch/Evidence 作为默认框架要求；只有第二协议出现相同研究问题且有证据时再显式选择。

## 32. 最终代码冻结建议

建议把当前状态标记为“Raft 方法冻结基线”：

1. 禁止静默改变任何稳定 key、schema、Goal/Branch/Evidence ID；
2. 新实验从当前 mainline manifest 派生，不复用 excluded artifact；
3. Branch/Evidence 只修兼容性缺陷，不再扩展方法；
4. focused 只修明确 bug，不增加新阶段；
5. 重构巨型文件前先建立 byte-for-byte artifact corpus；
6. 发布 artifact 时固定 Go、raft commit、TLC profile、manifest、seed 和命令；
7. 下一轮若开始 Facet Corpus 实验或第二协议迁移，应作为独立任务，不在本轮自动展开。

## 附：代码与文档不一致记录

1. README 的 v1 模块概览仍列出可选 LLM 模块；这描述系统能力，不等于当前论文方法。本轮新增冻结主线入口以消除歧义。
2. “focused 是论文主线”不等于“CLI 默认开启 focused”。代码默认 advisor off 是冻结兼容行为，正式 focused manifest 显式开启。
3. Branch/Evidence 默认不指导搜索，但兼容 artifact 仍可能被写出；“关闭”指不影响决策，不是完全不生成文件。
4. 第六轮早期目录在报告中被明确排除，但对应 manifest 保留，因为排除原因本身是研究证据。

## 附：验证环境说明

提示给出的空 `/tmp` GOPATH 会失去已有 module cache，并触发联网下载，与本轮“不联网安装”和 sibling raft replace 冲突。实际使用：

```text
GOTOOLCHAIN=local
GOCACHE=/tmp/modelfuzz-ng-cleanup-{before,after}-gocache
GOPATH=/home/test/go
```

Go 为 1.26.4。`go list ./...` 得到 25 个项目包，`go list -deps ./...` 得到 253 个包；最终 build、test、race、vet、JSON parse、gofmt 和 `git diff --check` 均通过。

---

# 附录 A：修改前代码与文件清单

## A.1 基线

| 项目 | 数值 |
|---|---:|
| 工作树文件（不含 `.git/`） | 305 |
| Go 文件（`cmd/` + `internal/`） | 155 |
| 非测试 Go 文件 | 100 |
| Go 测试文件 | 55 |
| Go 总行数 | 43,511 |
| 非测试 Go 行数 | 30,598 |
| 测试 Go 行数 | 12,913 |
| 修改前 Markdown 行数 | 8,755 |
| `examples/` 顶层 JSON | 28 |
| Go 项目包 | 25 |
| `go list -deps` 依赖包 | 253 |

物理行口径包含注释和空行。最初无过滤 `find` 得到 331 个文件，其中 26 个属于 `.git/`；正式前后比较均排除 `.git/`。

## A.2 模块分类与删除判断

| 类别 | 主要路径 | 默认状态 | 论文定位 | Artifact/测试依赖 | 整理决定 |
|---|---|---|---|---|---|
| 协议无关核心 | `internal/core`、`plan`、`runtime`、`engine` | 默认启用 | 执行基础 | Trace、checkpoint、广泛单测 | 保留，高风险 |
| Replay/ddmin | `internal/trace`、`minimize` | 显式命令 | 可靠性基础 | 历史 failure/Trace | 保留，高风险 |
| Corpus/Experiment | `internal/corpus`、`experiment`、`persistence` | 默认 fuzz | 实验基础 | Corpus、checkpoint、报告 | 保留 |
| etcd-raft 适配 | `internal/adapters/etcdraft` | 默认启用 | 首协议执行层 | 生命周期、Snapshot、mutant tests | 保留 |
| Raft model/Oracle | `internal/model/raft`、`oracle/raft` | 默认/strict TLC | 语义与错误检测 | v1/v2/Facet/Mapper/failure | 保留 |
| Facet | `coverage_facets.go`、`coverageanalysis` | 离线/Goal artifact | 当前主线 | Facet schema 与统计 | 保留 |
| Goal/Waypoint | `goalsearch/schema.go`、`evaluator.go` | 显式 goal-search | 当前主线 | Goal artifact、在线/离线 tests | 保留 |
| Standard Frontier | `goalsearch/frontier.go`、`mutation.go` | 默认 goal-search | 当前主线 | prefix/replay tests | 保留 |
| Focused mutation | `protocolmutation/`、`protocolmutation/raft` | 显式开启 | 当前主线 | Advisor JSONL/summary/tests | 保留 |
| Branch/Evidence | `goalsearch/branch*`、`evidence*` | 默认不指导搜索 | 冻结实验 | 第五、六轮 artifact/负结果 | keep-experimental |
| 实验 CLI | `goal_benchmark.go`、`goal_compare.go` | 显式命令 | 基础设施 | 正式 manifest/CSV | 保留 |
| C=2/Failure 分析 | `goal_c2_analysis.go`、`formation_failure.go` | 显式诊断 | 冻结实验 | 第六轮分析 | 隐藏默认入口，保留实现 |
| v1/v2/旧 mode | coverage/兼容 flags/readers | 兼容读取 | 方法演化 | 历史 artifact | keep-deprecated |

没有发现 `init()`、Go build tag 或反射注册形成的隐藏实验策略。CLI 使用显式 switch 和 flag 注册。

## A.3 重点文件规模

| 模块 | 非测试行 | 测试行 | 合计 |
|---|---:|---:|---:|
| `internal/core` | 1,099 | 595 | 1,694 |
| `internal/plan` | 620 | 412 | 1,032 |
| `internal/runtime` | 958 | 705 | 1,663 |
| `internal/engine` | 563 | 439 | 1,002 |
| `internal/experiment` | 1,888 | 776 | 2,664 |
| `internal/adapters/etcdraft` | 1,699 | 1,070 | 2,769 |
| `internal/model/raft` | 2,915 | 1,770 | 4,685 |
| `internal/goalsearch` | 6,813 | 1,684 | 8,497 |
| `internal/protocolmutation` | 288 | 51 | 339 |
| `internal/protocolmutation/raft` | 647 | 329 | 976 |
| `cmd/modelfuzz-ng` | 6,837 | 2,576 | 9,413 |

## A.4 测试和 fixture

55 个 `_test.go` 文件覆盖 core/plan/runtime/engine、etcd-raft 生命周期、Mapper/TLC/Oracle、v1/v2/Facet、Goal/Waypoint/Frontier、Branch/Evidence/Advisor、CLI/checkpoint/replay/ddmin 和 benchmark。

`examples/plans/` 的 17 个 Plan 是运行、replay、生命周期和故障 fixture，全部保留。仓库没有独立 `golden/` 目录；golden 期望主要以内联常量和精确 JSON/字符串断言存在。

---

# 附录 B：研究路线、Manifest 与 Artifact 索引

## B.1 主线阅读顺序

1. [`semantic-coverage-factorization.md`](semantic-coverage-factorization.md)：Facet；
2. [`manual-behavior-goals-and-waypoints.md`](manual-behavior-goals-and-waypoints.md)：Goal/Waypoint；
3. [`waypoint-frontier-validation-and-bug-detection.md`](waypoint-frontier-validation-and-bug-detection.md)：Frontier、replay 和缺陷检出；
4. [`focused-protocol-aware-mutation-and-method-freeze.md`](focused-protocol-aware-mutation-and-method-freeze.md)：focused mutation 和方法冻结。

设计演化保留 [`v1-baseline.md`](v1-baseline.md) 与 [`semantic-coverage-v2-prototype.md`](semantic-coverage-v2-prototype.md)。

## B.2 冻结实验阅读顺序

1. [`behavior-branch-diversity-and-frontier.md`](behavior-branch-diversity-and-frontier.md)；
2. [`c2-success-seed-differential-analysis.md`](c2-success-seed-differential-analysis.md)；
3. [`partial-branch-evidence-and-stage-budgeting.md`](partial-branch-evidence-and-stage-budgeting.md)；
4. [`branch-evidence-freeze.md`](branch-evidence-freeze.md)。

研究证据总账为 [`research-evidence-ledger.md`](research-evidence-ledger.md)。

## B.3 正式与 Pilot manifest

| 轮次 | 类型 | Manifest |
|---|---|---|
| 第四轮 | 稳定性/消融 | `examples/goal-benchmark-direction-a-stability.json` |
| 第四轮 | mutant/control | `examples/goal-benchmark-direction-a-mutants.json` |
| 第五轮 | Pilot | `examples/goal-benchmark-branches-pilot.json` |
| 第五轮 | 正式稳定性 | `examples/goal-benchmark-branch-diversity-stability.json` |
| 第五轮 | 消融 | `examples/goal-benchmark-branch-diversity-ablations.json` |
| 第五轮 | mutant/control | `examples/goal-benchmark-branch-diversity-mutants.json` |
| 第六轮 | C=2 差分 | `examples/goal-benchmark-round6-c2-differential.json` |
| 第六轮 | 单 Branch | `examples/goal-benchmark-round6-single-branch-reachability.json` |
| 第六轮 | Pilot/E2/深度 | `goal-benchmark-round6-evidence-*.json` |
| 第六轮 | 正式 | `examples/goal-benchmark-round6-formal.json` |
| 第六轮 | mutant/control | `examples/goal-benchmark-round6-mutants.json` |
| 第七轮 | Pilot | `examples/goal-benchmark-round7-focused-pilot.json` |
| 第七轮 | 正式 M0–M4 | `examples/goal-benchmark-round7-focused-formal.json` |
| 第七轮 | 消融 | `examples/goal-benchmark-round7-focused-ablations.json` |
| 第七轮 | M3 修正 | `examples/goal-benchmark-round7-no-quorum-correction.json` |
| 第七轮 | mutant/control | `examples/goal-benchmark-round7-focused-mutants.json` |

全部 18 个 benchmark manifest 都是有效 JSON，schema 均为 `raft-goal-benchmark-v1`。

## B.4 Accepted artifact

| 内容 | 本机 Artifact 根目录 |
|---|---|
| 第六轮 C=2 分析 | `/tmp/modelfuzz-ng-round6-c2-analysis-v4-20260728` |
| 第六轮单 Branch | `/tmp/modelfuzz-ng-round6-single-branch-v3-20260728` |
| 第六轮 Pilot | `/tmp/modelfuzz-ng-round6-evidence-pilot-v4-20260728` |
| 第六轮正式 | `/tmp/modelfuzz-ng-round6-formal-v4-20260728` |
| 第六轮 mutants | `/tmp/modelfuzz-ng-round6-mutants-v1-20260728` |
| 第七轮 Pilot | `/tmp/modelfuzz-ng-round7-focused-pilot-v1-20260729` |
| 第七轮正式 | `/tmp/modelfuzz-ng-round7-focused-formal-v1-20260729` |
| 第七轮消融 | `/tmp/modelfuzz-ng-round7-focused-ablations-v1-20260729` |
| 第七轮 M3 修正 | `/tmp/modelfuzz-ng-round7-no-quorum-corrected-v1-20260729` |
| 第七轮 mutants | `/tmp/modelfuzz-ng-round7-focused-mutants-v1-20260729` |
| Snapshot ddmin | `/tmp/modelfuzz-ng-round7-ddmin-snapshot-v1` |
| Restart ddmin | `/tmp/modelfuzz-ng-round7-ddmin-restart-v1` |

第七轮正式 replay 1,205/1,205、Pilot 337/337、消融 798/798、mutant 1,235/1,235、M3 修正 145/145，均为 mismatch 0。`/tmp` 是当前机器证据位置；论文 artifact 发布时应复制到版本化只读介质并记录摘要。

## B.5 被排除但保留记录的实验

- `/tmp/modelfuzz-ng-round6-c2-differential-raw-20260728`：布尔默认继承导致不完整；
- `/tmp/modelfuzz-ng-round6-formal-20260728`：priority multiplier 未显式保存；
- `/tmp/modelfuzz-ng-round6-formal-v2-20260728`：Evidence stable key 浅拷贝问题；
- `/tmp/modelfuzz-ng-round6-formal-v3-20260728`：显式 false 未正确覆盖 true；
- `/tmp/modelfuzz-ng-round6-evidence-depth-pilot-v3-20260728`：不属于最终公平环境。

它们不能用于正式结论，但用于防止挑选性报告和解释设计修正，因此相关 schema、manifest 和读取代码不能删除。

---

# 附录 C：清理决策表

## C.1 实际处理

| 文件/符号 | 当前引用与依赖 | 决定 | 理由 | 验证 |
|---|---|---|---|---|
| `goal_search.go: hasChange` | 只有声明；无 schema/artifact/test/docs | delete | 满足私有死代码条件 | 全测、race、Goal 回归 |
| root usage 的 C=2 列表项 | 命令实现和历史分析仍被使用 | hide-only | 冻结诊断不应出现在默认主线帮助 | root help、显式命令、全测 |
| Branch/Evidence 子系统 | 多个 CLI、schema、第五/六轮 artifact | keep-experimental | 保留负结果和兼容重算 | record-only、schema tests |
| v1/v2 coverage | coverage analysis、历史实验 | keep-deprecated | 历史 schema 和抽象演化 | golden/coverage compare |
| `branch-budget-allocation` | 历史 manifest/settings | keep-deprecated | 删除会破坏复现 | manifest/CLI tests |
| 第六轮 manifest | 正式和被排除实验说明 | keep-experimental | 排除原因也是研究证据 | JSON/schema parse |
| CSV 辅助实现 | figure-ready artifact | keep | 原子性、错误和顺序并不等价 | 延期至 byte-golden |
| 巨型文件拆分 | 主线/兼容 wiring 高度交织 | keep | 大量无语义 diff，冻结轮风险高 | 延期 |

## C.2 操作统计

- 删除文件：0；
- 删除私有符号：1；
- 默认帮助隐藏冻结命令：1；
- 合并文件：0；
- 移动文件：0；
- schema/CLI 参数删除：0；
- manifest 删除：0。

## C.3 尚未处理的高风险候选

1. `goal_search.go` 同时承担 CLI、循环、checkpoint 和 artifact；
2. `evaluator.go` 同时实现两个 Goal 的在线/离线求值；
3. Branch/Evidence 与主线类型仍在同一 package；
4. 多个 CSV generator 有相似结构；
5. benchmark manifest 默认继承字段较多。

只有在建立完整 artifact byte-golden、CLI help golden 和历史 artifact corpus 后，才适合继续处理。

---

# 附录 D：整理前后规模与耦合

## D.1 总量

| 指标 | 整理前 | 整理后 | 变化 |
|---|---:|---:|---:|
| 工作树文件 | 305 | 307 | +2（总报告和审计 JSON） |
| Go 文件 | 155 | 155 | 0 |
| 非测试 Go 文件 | 100 | 100 | 0 |
| Go 测试文件 | 55 | 55 | 0 |
| Go 总行数 | 43,511 | 43,525 | +14 |
| 非测试 Go 行数 | 30,598 | 30,612 | +14 |
| 测试 Go 行数 | 12,913 | 12,913 | 0 |
| 导出声明（近似文本口径） | 372 | 372 | 0 |
| 构建二进制 | 17,460,096 B | 17,460,096 B | 0 |

合并后只保留本报告和 `cleanup-candidates.json` 两个本轮新增文件，因此工作树文件最终为 307，而不是合并前的 311。

## D.2 研究模块

| 模块 | 非测试行 |
|---|---:|
| Facet | 496 |
| Goal/Waypoint/Standard Frontier/Mutation | 3,660 |
| focused 通用 Advisor | 288 |
| Raft focused Advisor | 647 |
| Branch/Evidence 冻结实验 | 3,166 |
| CLI/Artifact/Benchmark | 6,838 |
| 离线 coverage analysis | 1,481 |

## D.3 通用与 Raft 专用比例

- 协议无关核心：9,672 行；
- etcd-raft 适配 + Raft model/Oracle/Advisor：5,520 行；
- 合计中通用约 63.7%，Raft 专用约 36.3%；
- focused mutation 内部，通用接口占 30.8%，Raft Advisor 占 69.2%。

通用接口只承担“当前状态到合法候选、解释和可重算 summary”；消息类别、term、quorum、Snapshot boundary 和 restart 时机必须留在协议插件。

## D.4 耦合结论

1. `internal/protocolmutation` 只依赖 `core` 和 `plan`；
2. `internal/protocolmutation/raft` 承担 focused Raft 判断；
3. `goalsearch/mutation.go` 只面向通用 Advisor；
4. 具体 Raft wiring 位于 CLI；
5. 本轮没有把 Raft 判断加入 core/runtime/engine；
6. 整理专属改动没有改变任何公开接口。
