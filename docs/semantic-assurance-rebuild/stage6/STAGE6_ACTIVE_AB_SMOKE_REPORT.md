# Stage 6 Current Baseline vs Facet-only 主动 A/B Smoke 报告

## 1. 结论

- 机制结论：**GO**
- 性能方向：**SIGNAL_NEGATIVE**
- 基线：branch `agent/semantic-assurance-rebuild-v1-stage4`，
  HEAD `d281e2f2fdaba85a8e45d227f6342f93849eb5e0`
- 正式主比较：3 seeds × 2 modes，candidate budget 均为 48；因预注册的 FIFO
  自然耗尽规则，实际执行 258 个 candidate。
- seed 6601 的两个 mode 各完整重复一次，candidate lineage、digest、admission、
  Decision、coverage、queue、Corpus digest 和 Facet state digest 逐字段一致。
- 所有正式 candidate 均为 `completed`；Runtime/Mapper/TLC/Oracle harness failure
  为 0，Oracle finding 为 0，Facet `invalid_evidence` 和
  `insufficient_evidence` 均为 0。

机制 GO 只说明 Facet Breadth 决策能够公平且确定地控制真实 candidate stream，不说明
Facet 优于 current baseline。

## 2. 实现方式与边界

新增代码全部为 `package raft_test`：

- `internal/assurance/facet/raft/active_ab_helpers_test.go`
- `internal/assurance/facet/raft/active_ab_campaign_test.go`
- `internal/assurance/facet/raft/active_ab_smoke_test.go`

测试复用 Stage 4 的 `recordingSource`、`executeRealEtcdRaft`、
`staticPlanSource` 和真实 Runtime/Adapter/Mapper/Oracle 构造。每个 candidate 通过
`experiment.Runner.RunFeedback` 的单 candidate 边界获得真实 `Completion`，
随后才调用 `executionrecord.BuildV1`、`facet.EvaluateAll` 和两套 shadow feedback。

未修改生产代码、Runner、Corpus、mutator、Facet、Facet Breadth、TLC、Oracle、
Artifact、CLI 或 Stage 0—5 文件。未写 run artifact，未运行 replay，未使用 mutant。

## 3. Production mutator 审计

`internal/mutation/random.go` 的 `Random.Mutate`：

- 输入为 `mutation.Request{Entry, Count, Seed}`；
- 读取 parent `Entry.Plan` 和稳定 Entry metadata；
- 使用 `rand.New(rand.NewSource(request.Seed))`，不读取 wall-clock 或全局 RNG；
- 不读取 raw/semantic coverage、Corpus admission 或 Facet；
- 在修改前调用 `Plan.Copy`，不原地修改 parent；
- 输出顺序由显式 seed 和请求顺序确定。

Harness 对每个 child slot 分别以 `Count=1` 调用同一 production mutator。seed 只由
campaign seed、parent lineage 和 child slot 派生。快速测试和 strict 重叠 lineage
检查均证明相同 lineage 产生相同 child。

## 4. 正式 strict TLC 环境

| 项 | 值 |
|---|---|
| Server | `modelfuzz-ng-tlc` v1, `strict=true` |
| TLA+ Tools | 1.8.0，冻结 SHA-256 `cc4803d...e516b3` |
| Java | OpenJDK Temurin 17.0.20+8 |
| Model | `models/raft/raft_storage_snapshot.tla` |
| Config | `models/raft/raft-storage-snapshot-10.cfg` |
| Profile | `storage-snapshot` |
| Server | `{1,2,3}` |
| MaxValue / Nil | 5 / 0 |
| MaxLogIndex / LargestTerm | 10 / 10 |
| Snapshot threshold / retain | 3 / 1 |
| PreVote / CheckQuorum | false / false |
| 端口 | 独占本地 `127.0.0.1:18761` |

新目录缺少下载缓存；未访问外网，而是复用
`/home/test/Desktop/modelfuzz-ng-partition` 中 SHA-256 完全匹配的 1.8.0 jar。构建产物
只位于 `.gitignore` 已排除的 `tools/tlc-server/.cache` 和 `build`。Sandbox 内首次
监听被拒绝，随后在允许本地回环的相同代码状态运行。

最终 TLC 累计 metrics（包含首次诊断运行和正式重跑）：

- requests/succeeded/failed：780 / 780 / 0；
- model events：15,571；
- action lookups：15,571；
- server `errors_by_code`：空。

本阶段启动的服务在验证后以 SIGINT 关闭，exit 130；未干扰其他进程。

## 5. Initial population

每个 seed 的六个 Plan 只生成一次并 deep-copy 给两个 mode。Slots 0—2 为固定 example，
slots 3—5 为 recording ActionSource 产生的真实高层 Plan。

| Seed | slot 0 | slot 1 | slot 2 |
|---:|---|---|---|
| 6601/6602/6603 | `9c964dbe...e6a42` | `16e1d5e...65d5f` | `1eac4135...b6383` |

| Seed | slot 3 Snapshot success | slot 4 FailFirst | slot 5 Random |
|---:|---|---|---|
| 6601 | `52e5b36e...3e9e` | `7fcffb81...2a2` | `2f62ba9e...dc17` |
| 6602 | `4b8e417a...e487` | `ed077548...cbe5` | `92ea3e26...3cb3` |
| 6603 | `a9e31d3c...4d72` | `80a9cf9d...50c6` | `c2b03408...5650` |

同 seed 的两个 mode 六项 digest 完全相同；全部 Plan 在比较前通过 `Validate`，且不超过
40 个 PlanAction。

## 6. Campaign 指标

`B` 为 current-baseline，`F` 为 facet-only。

| Seed/mode | executed | queue exhausted | Plan/Concrete/Trace | events/states | unique Plan/Trace/Path |
|---|---:|---|---:|---:|---:|
| 6601 B | 40 | yes | 864 / 743 / 743 | 857 / 897 | 40 / 40 / 26 |
| 6601 F | 48 | no | 978 / 833 / 833 | 916 / 964 | 47 / 40 / 26 |
| 6602 B | 48 | no | 1178 / 947 / 947 | 957 / 1005 | 46 / 48 / 25 |
| 6602 F | 46 | yes | 876 / 800 / 800 | 819 / 865 | 45 / 46 / 23 |
| 6603 B | 48 | no | 1108 / 1062 / 1062 | 1040 / 1088 | 47 / 48 / 30 |
| 6603 F | 28 | yes | 490 / 465 / 465 | 537 / 565 | 26 / 28 / 19 |

| Seed/mode | duplicate Plan | duplicate Trace | duplicate Path | raw / sem-state / sem-transition |
|---|---:|---:|---:|---:|
| 6601 B | 0.000 | 0.000 | 0.350 | 202 / 185 / 247 |
| 6601 F | 0.021 | 0.167 | 0.458 | 205 / 188 / 252 |
| 6602 B | 0.042 | 0.000 | 0.479 | 208 / 182 / 242 |
| 6602 F | 0.022 | 0.000 | 0.500 | 247 / 195 / 259 |
| 6603 B | 0.021 | 0.000 | 0.375 | 181 / 157 / 225 |
| 6603 F | 0.071 | 0.000 | 0.321 | 146 / 139 / 189 |

所有 ratio 以 candidate 数为分母；candidate 内 coverage 已去重。

## 7. Admission、Facet 与主动结构

| Seed/mode | baseline retained | Facet admitted | Facet keys E/R/S | reasons new/no/shorter | active parents | children generated/executed | max depth/final queue |
|---|---:|---:|---:|---:|---:|---:|---:|
| 6601 B | 17 | 16 | 7/4/7 | 7/24/9 | 17 | 34/34 | 3/0 |
| 6601 F | 18 | 24 | 7/4/7 | 7/24/17 | 24 | 48/42 | 5/6 |
| 6602 B | 21 | 13 | 6/4/7 | 6/35/7 | 21 | 42/42 | 6/0 |
| 6602 F | 20 | 20 | 6/4/7 | 6/26/14 | 20 | 40/40 | 6/0 |
| 6603 B | 23 | 11 | 7/4/7 | 7/37/4 | 23 | 46/42 | 5/4 |
| 6603 F | 16 | 11 | 7/4/7 | 7/17/4 | 11 | 22/22 | 3/0 |

`new_and_shorter` 与 `ineligible_evidence` 均为 0。`no_novelty` 和
`shorter_representative` 均自然出现，因此没有
`FACET_DECISION_DEGENERATE`。每个 admitted parent 恰好生成两个 child；未执行的
child 只因固定预算截断。

Observed snapshot classes 在所有 campaign 都是：

`snapshot_created`、`log_compacted`、`snapshot_sent`、`snapshot_delivered`、
`snapshot_applied`、`snapshot_status_succeeded`、`snapshot_status_failed`。

Election 每 campaign 观察 6 或 7 类，Replication 均观察 4 类。首次 snapshot
discovery 固定发生在 initial slot 3（ordinal 3），failure status 在 slot 4
（ordinal 4）。所有 key 的完整 first-discovery ordinal 由测试的 compact canonical
JSON 输出记录并用于重复性比较。

## 8. Representative

| Seed/mode | distinct First/Shortest refs | First actions avg/median | Shortest actions avg/median |
|---|---:|---:|---:|
| 6601 B | 7 / 9 | 18.22 / 25 | 17.83 / 24 |
| 6601 F | 7 / 8 | 18.22 / 25 | 17.67 / 24 |
| 6602 B | 6 / 7 | 16.94 / 25 | 16.76 / 25 |
| 6602 F | 6 / 7 | 16.94 / 25 | 16.53 / 25 |
| 6603 B | 7 / 8 | 16.22 / 17.5 | 15.33 / 17.5 |
| 6603 F | 7 / 8 | 16.22 / 17.5 | 15.33 / 17.5 |

First/Shortest 是按 Facet key 逻辑 slot 统计后的 distinct `RecordDigest` 引用，不保存
candidate history。

## 9. 公平性与重复性

| Seed | overlap lineages | baseline-exclusive | facet-exclusive |
|---:|---:|---:|---:|
| 6601 | 28 | 12 | 20 |
| 6602 | 20 | 28 | 26 |
| 6603 | 26 | 22 | 2 |

所有 overlap lineage 的输入 Plan、execution seed、PlanDigest、Engine/Experiment
status、TraceDigest、ModelStatePathDigest、Oracle codes、Facet evaluations 和
semantic projection 完全相同。

seed 6601 的重复运行验证了：

- lineage sequence 与 child lineage；
- per-lineage Plan/Trace/model-path digest；
- active admission、Facet Decision；
- coverage sets、metrics；
- Facet StateDigest、Corpus digest；
- final queue。

以上全部一致。时间与 Java debug timing 不参与比较。

## 10. 为什么是 SIGNAL_NEGATIVE

三 seed 主比较的 unique TraceDigest 合计：

- current-baseline：136；
- facet-only：114。

Facet-only 在 6602（46/48）和 6603（28/48）提前耗尽；6603 的 raw/semantic
coverage 与执行深度也更低。6601 中 Facet-only 执行满预算，但 duplicate Trace ratio
为 0.167，而 baseline 为 0。

Facet-only 在 6602 的 raw/semantic coverage 略高，6601 的 semantic coverage 也略高，
因此信号并非所有指标同向；但预注册分类将明显的早耗尽、合计 unique trace 降低和
关键 seed 行为减少归为 `SIGNAL_NEGATIVE`。未修改 Facet classes、initial population、
budget、mutator 或 energy 来修正结果。

## 11. 限制与 Stage 7

- 仅三个 development seeds，小预算 Smoke，不是统计结论；
- 没有 mutant，不能评价 bug detection；
- snapshot initial population 已覆盖 7 个常见 lifecycle class；
- candidate budget 是相同上限，实际执行数可因预注册 queue exhaustion 不同；
- 没有 artifact/replay/minimize 评价；
- 没有测试 Goal、Agent、hybrid 或 energy。

机制允许进入 Stage 7，但 Stage 7 必须把 `SIGNAL_NEGATIVE` 作为预注册输入，并设置
停止门槛；不得在 held-out 结果之后修改 Facet v1 class。
