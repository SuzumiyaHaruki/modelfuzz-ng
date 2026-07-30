# Raft Semantic Coverage v2 Prototype：设计与评估

> Schema：`raft-coverage-v2-prototype`
> 状态：实验性并行投影，不替换 `raft-coverage-v1`，不参与当前 Corpus 准入。

## 1. 本轮范围

本轮只处理“状态粒度”：

- 保留现有 Raw TLC State 和 `raft-coverage-v1`；
- 新增一个更粗、节点对称、无绝对 term/index 的 v2 prototype；
- 新增只读离线比较；
- 不修改 Raft 执行、strict TLC、Oracle、Mutation、候选调度或默认 Corpus；
- 不实现 LLM 反馈、Behavior Goal、coverage stall 或新的因果 Semantic Transition。

## 2. 修改前的覆盖数据流

现有在线路径如下：

1. `internal/engine` 执行真实 etcd-raft，并保存 `Trace`、模型事件和 TLC 模型状态；
2. `internal/experiment/runner.go` 在一次成功执行后调用
   `internal/model/raft.ProjectCoverage(states, events)`；
3. `internal/model/raft/coverage.go` 的 `projectState` 构造
   `raft-coverage-v1` 字符串，并对字符串做 SHA-256 截断得到 `int64` key；
4. v1 transition key 为 `pre-state + event name + post-state`；
5. `internal/corpus` 用 Raw key、v1 state key 和 v1 transition key 做全局 novelty
   及 Corpus 准入；
6. `runs.jsonl` 保存每次执行的新增数量，Corpus/checkpoint 保存 v1 key 集合；
7. 只有逐运行 artifact 被保存时，`model-states.json` 才包含可离线重算 v1/v2
   的完整 TLC 状态文本。仅有 `runs.jsonl` 或聚合 report 时不能重建 v2。

当前 Trace v1 保存每一步的节点 Observation，但不保存每一步完整消息队列和
`NetworkPartition`，而且一个 concrete step 可能映射为多个模型事件。因此，本轮离线
比较以逐运行 `model-states.json` 为权威输入，不从不完整 Trace 猜测模型状态。缺少
逐运行模型状态的历史实验会明确报告为不可重算，而不会静默使用聚合数字替代。

## 3. v1 过细的主要来源

`raft-coverage-v1` 有价值，但以下字段会持续扩大 key 空间：

- 按原始节点顺序保存 active set、role、term、commit、vote 和 storage 分类；
- 完整保存每个节点的归一化日志序列，包括逐 entry term 与跨日志 value 等价类；
- 按具体 leader/peer 位置保存 replication progress；
- storage key 同时保存每个节点的 applied/snapshot/firstIndex 关系及每条 leader
  progress；
- transition 把较细的前后状态同时拼入 key。

## 4. v2 prototype schema

每个状态先构造成固定字段顺序的 Go struct，再使用标准库 `json.Marshal` 生成无空白
JSON，最后沿用 v1 的 SHA-256 前 64 bit `int64` key。最终 JSON 的顶层字段严格为：

| 字段 | 内容 |
|---|---|
| `schema` | 固定为 `raft-coverage-v2-prototype` |
| `cluster_size` | 节点总数；3 节点与 5 节点不合并 |
| `quorum_available` | active 节点数是否达到 `n/2+1` |
| `role_topology` | `active/crashed × follower/candidate/leader` 的排序计数 |
| `term_topology` | active 节点的有限 term 关系分类 |
| `leader_term_position` | active leader 是否位于 active 最大 term |
| `candidate_term_position` | active candidate 是否位于 active 最大 term |
| `log_topology` | 日志相等、前缀分叉、未提交后缀冲突或已提交冲突 |
| `committed_prefixes` | `agree` 或 `conflict` |
| `catch_up_topology` | leader 到 peer 的 coarse catch-up 类别计数 |
| `snapshot_topology` | 各节点 coarse snapshot/storage 阶段计数 |
| `voting_topology` | candidate 获票和 votedFor 关系计数 |
| `canonical_node_shapes` | 不含节点 ID 的节点描述，排序后按相同描述计数 |

每个 canonical node shape 包含：

- `lifecycle`：`active` / `crashed`；
- `role`：`follower` / `candidate` / `leader`；
- `term_position`：`max` / `behind-one` / `behind-multiple`；
- `log_relation`：相对规范参考日志的 `equal` / `prefix` / `extends` / `conflict`；
- `commit_lag`；
- `applied_lag`，Basic profile 记为 `not-modeled`；
- `snapshot_phase`；
- `voted_for` 和 `candidate_votes`；
- 作为 leader 时对 peer 的复制 lag 多重集；
- 作为 peer 时来自 active leader 的 catch-up 类型多重集。

所有类别集合都序列化为按类别字典序排列的 `{class,count}` 数组，不使用 Go map 的
迭代顺序。

### 4.1 term 分类

顶层 `term_topology` 只看 active 节点：

- `no-active-nodes`；
- `all-same`；
- `one-node-one-term-ahead`；
- `one-node-multiple-terms-ahead`；
- `split-terms`。

每个节点相对所有节点（包括 crashed 节点）的最大 term 只记：

- `max`；
- `behind-one`；
- `behind-multiple`。

绝对 term 数字不进入最终 JSON。

### 4.2 lag 分类

阈值只在 `PrototypeLagSmallMax=3` 一处定义：

| 差值 | 类别 |
|---:|---|
| 0 | `zero` |
| 1 | `one` |
| 2–3 | `small` |
| 4+ | `large` |

commit lag、applied lag 和 replication lag 都复用该函数。

### 4.3 日志和追赶分类

完整日志只在投影过程中临时用于判断关系，不写入 key：

- 所有日志相同：`all-equal`；
- 只有长度不同且都满足前缀关系：`prefix-divergence`；
- 分歧仅位于共同 committed prefix 之后：`uncommitted-suffix-divergence`；
- 共同 committed prefix 内已经分歧：`committed-conflict`。

Storage/Snapshot profile 还将 leader→peer 关系分类为：

- `caught-up`；
- `append-one` / `append-small` / `append-large`；
- `snapshot-required`：leader 的 `nextIndex < firstIndex`；
- `snapshot-pending`：`pendingSnapshot > 0`。

绝对 commit/applied/last/first/snapshot/match/next index 均不进入最终 JSON。

## 5. 节点规范化

规范化步骤为：

1. 严格解析 TLC state；缺少必需变量、tuple 维度不一致、越界 votedFor、commit
   超过日志长度或 storage 边界非法都会返回错误；
2. 优先选择 active、最大 term leader 的日志作为关系参考；多个同类 leader 时选择
   “最长、再按归一化 entry signature 字典序最小”的日志；没有 leader 时在所有日志
   中使用同一规则；
3. 对每个节点生成上节列出的 node shape，所有跨节点引用只转成角色、term 关系、
   vote 类别和 lag/catch-up 多重集；
4. node shape 先稳定 JSON 序列化，再字典序排序；
5. 完全相同的 shape 合并为 `{class,count}`；
6. 顶层固定字段 JSON 加 schema 后进行 hash。

节点 ID 只在解析 tuple、set 和矩阵引用时作为临时位置使用，不进入最终 key。测试覆盖
了 leader/follower 及其 matchIndex/log 位置整体换名后 v2 key 不变，同时确认同一对
状态在 v1 中仍不同。

## 6. 有意删除和保留的信息

有意删除：

- 原始节点 ID 和 tuple 位置；
- 绝对 term；
- 完整日志 entry 序列及每个 client value 的等价类序列；
- 绝对 index；
- 每个具体 leader→peer ID 对；
- election/heartbeat timer 的具体值；
- v1 transition 的前后状态拼接。

保留：

- active/crashed、角色拓扑和多数派可用性；
- active term 拓扑，以及 leader/candidate 是否处于最大 term；
- 日志相等、前缀、未提交冲突和已提交冲突；
- commit/applied/replication lag bucket；
- candidate 的 self-only、one-short、quorum-reached 等获票边界；
- coarse votedFor 目标关系；
- Snapshot 创建/压缩、普通复制追赶、需要 Snapshot、pending Snapshot；
- Basic 与 Storage/Snapshot profile 的区别。

`quorum_available` 目前只表示 active 节点数量达到多数派，不表示某个网络分区组拥有
多数派。原因是 Partition/Heal 在当前 TLA+ 模型中是 stutter，模型 state 不含网络
分区。恢复节点的 epoch 也不在模型 state 中，因此 v2 能区分 crashed 与 active，
但不能区分“刚 restart 的 recovering follower”和同状态的普通 active follower。
本轮按要求没有扩展 Observation/Trace schema 来填补这两个缺口。

## 7. 离线比较

命令：

```bash
go run ./cmd/modelfuzz-ng coverage-compare \
  -input /path/to/experiment-or-run \
  -output /tmp/coverage-comparison.json
```

输入可以是：

- 包含多个 `run-*/model-states.json` 的实验目录；
- 单个 run artifact 目录；
- 单个 `model-states.json`。

工具递归寻找并按路径排序这些文件。它不会修改输入；如果 `-output` 位于输入目录内，
命令会拒绝执行。不提供 `-output` 时只把 JSON 写到 stdout。

报告包含：

- v1/v2 schema；
- 模型状态访问总数；
- v1/v2 distinct state；
- `reduction_ratio=(v1-v2)/v1` 和 `compression_factor=v1/v2`；
- 每条执行的新增数及累计增长；
- 四个 quartile 的 v1/v2 新状态数；
- final quartile / first quartile novelty；
- v1-new-but-v2-old 执行数/比例；
- v2-new 执行数/比例；
- 对每份状态序列重复投影是否得到相同 v2 结果。

仅有 `runs.jsonl`、Trace 或聚合 report 不足以重建 TLC state。尤其 Trace v1 的
`StepRecord` 只保存节点快照，不保存每步完整分区/消息上下文，而且一个 step 可能展开
为多个模型事件。工具会明确报错，不会猜测或使用历史聚合数冒充重算结果。

## 8. 实际评估结果

仓库当前没有正式长跑的逐运行 `model-states.json`；正式随机基线使用
`artifact_policy=failures` 且没有失败，因此不能离线重算 v2。本轮没有引用其历史
v1 数量来伪造 v2 对比。

本轮在真实 etcd-raft、Storage/Snapshot TLA+ profile 和 strict TLC 上运行了两组小
实验。两组都只做随机/定向种子，不让 Corpus 变异影响输入顺序。

离线重算得到的 v1 distinct state 分别为 1,491 和 64，与两组原始
`experiment-report.json` 的 `unique_semantic_states` 完全一致。这是本轮没有改变 v1
投影、执行顺序或 artifact 解释方式的额外验证。

实际命令：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft_storage_snapshot.tla \
  --config models/raft/raft-storage-snapshot-10.cfg \
  --port 22031

GOCACHE=/tmp/modelfuzz-ng-coverage-v2-gocache \
GOPATH=/tmp/modelfuzz-ng-coverage-v2-gopath \
go run ./cmd/modelfuzz-ng experiment \
  -config examples/config-snapshot.json \
  -tlc http://127.0.0.1:22031 \
  -output /tmp/modelfuzz-ng-coverage-v2-real-20260728/random \
  -runs 24 -max-plan-actions 120 -artifact-policy all \
  -initial-population 1 -min-new-model-states 1000000 -seed 991000

GOCACHE=/tmp/modelfuzz-ng-coverage-v2-gocache \
GOPATH=/tmp/modelfuzz-ng-coverage-v2-gopath \
go run ./cmd/modelfuzz-ng experiment \
  -config examples/config-snapshot.json \
  -tlc http://127.0.0.1:22031 \
  -output /tmp/modelfuzz-ng-coverage-v2-real-20260728/snapshot-partition \
  -runs 8 -max-plan-actions 300 -artifact-policy all \
  -initial-policy snapshot-partition -initial-population 1 \
  -min-new-model-states 1000000 -seed 992000

GOCACHE=/tmp/modelfuzz-ng-coverage-v2-gocache \
GOPATH=/tmp/modelfuzz-ng-coverage-v2-gopath \
go run ./cmd/modelfuzz-ng coverage-compare \
  -input /tmp/modelfuzz-ng-coverage-v2-real-20260728/random \
  -output /tmp/modelfuzz-ng-coverage-v2-random-comparison-20260728.json

GOCACHE=/tmp/modelfuzz-ng-coverage-v2-gocache \
GOPATH=/tmp/modelfuzz-ng-coverage-v2-gopath \
go run ./cmd/modelfuzz-ng coverage-compare \
  -input /tmp/modelfuzz-ng-coverage-v2-real-20260728/snapshot-partition \
  -output /tmp/modelfuzz-ng-coverage-v2-snapshot-comparison-20260728.json
```

### 8.1 24-run 随机实验

配置：3 节点、seed 991000..991023、每条 120 PlanAction、snapshot threshold=2、
retain=0、artifact policy=all。

- 24/24 成功；
- 2,880 Action；
- 2,928 model event；
- 2,952 model state visit；
- 随机执行实际包含 Crash 23、Restart 21、Partition 73、Heal 70、BecomeLeader 36；
- 还包含 Snapshot create/compact 各 114、SendSnapshot 28、InstallSnapshot 18、
  FastForwardSnapshot 2。

离线状态结果：

| 指标 | v1 | v2 prototype |
|---|---:|---:|
| distinct state | 1,491 | 1,107 |
| 相对 v1 reduction | — | 25.75% |
| compression factor | — | 1.347× |
| Q1 新状态 | 369 | 313 |
| Q2 新状态 | 428 | 329 |
| Q3 新状态 | 391 | 295 |
| Q4 新状态 | 303 | 170 |
| Q4/Q1 | 0.821 | 0.543 |
| 有新状态的执行 | 24/24 | 24/24 |

v2 的后四分之一增长相对前四分之一下降得更明显，但 24 条执行每一条仍产生 v2 新
状态。因此这组数据只支持“比 v1 更粗，并出现更明显的降速”，不支持“已经饱和”。

### 8.2 8-run 定向 Snapshot/Partition 实验

配置：`snapshot-partition`、seed 992000..992007、artifact policy=all。

- 8/8 `policy_complete`；
- 每条都执行 Partition/Heal、CreateSnapshot、CompactLog、SendSnapshot、
  InstallSnapshot、RejectSnapshot 和 HandleSnapshotStatus；
- 共 184 Action、224 model event、232 model state visit。

| 指标 | v1 | v2 prototype |
|---|---:|---:|
| distinct state | 64 | 22 |
| 相对 v1 reduction | — | 65.62% |
| compression factor | — | 2.909× |
| Q1 新状态 | 43 | 22 |
| Q2 新状态 | 21 | 0 |
| Q3 新状态 | 0 | 0 |
| Q4 新状态 | 0 | 0 |
| Q4/Q1 | 0 | 0 |
| 有新状态的执行 | 3/8 | 1/8 |
| v1-new-but-v2-old | — | 2/8（25%） |

这里不同 seed 实际只形成 3 条唯一 model-state path。v2 在第一条执行后将其余重复高层
Snapshot 行为合并，符合本轮目标；但不能据此推断一般随机空间也会快速饱和。

### 8.3 聚焦性质

自动化测试确认：

- 同一状态重复投影稳定；
- 对称节点整体换名不改变 v2；
- 绝对 term 平移不改变 v2；
- 给所有日志增加共同前缀并同步平移 commit/applied/snapshot/first/match/next 后，
  v2 不变；
- 只增加具体日志长度但保持 topology 和 lag bucket 时，v1 不同、v2 相同；
- stable leader/no leader、quorum available/unavailable、active/crashed、
  ordinary catch-up/snapshot-required 仍不同；
- 5 节点 candidate 的 self-only、one-short、quorum-reached 仍不同，这保留了 quorum
  mutant 最关键的“票数边界”语义；
- v1 的一个固定序列化 golden string 保持不变；
- 离线分析重复计算结果一致。

旧人工 mutant 正式实验的完整逐运行模型状态不在当前仓库中，所以没有声称重新计算了
mutant 的 v2 数字。

验证命令：

```bash
GOCACHE=/tmp/modelfuzz-ng-coverage-v2-gocache \
GOPATH=/tmp/modelfuzz-ng-coverage-v2-gopath \
go test ./...
```

## 9. 已知限制与下一轮建议

限制：

- v2 仍将多个 coarse 字段组合成完整笛卡尔积；随机实验 24/24 均有新 v2 状态，说明
  它可能仍然偏细；
- 没有分区拓扑和 recovering epoch；
- 参考日志用于生成每个节点的 `log_relation`；异常多 leader/no leader 状态虽然采用
  确定规则，但其类别是否最符合测试语义仍需更多样例；
- 当前只定义 v2 state，没有 v2 transition；
- 评估规模很小，且没有正式长跑 artifact；
- “更少”不等于“更好”，本轮只验证了若干必须保留的区别，没有证明所有真实 bug
  所需区别都保留；
- 64-bit hash 理论上存在碰撞风险，与 v1 相同；稳定 JSON 可用于调试，但分析报告当前
  只统计 hash key。

结论与建议：

> 下一轮应先继续细化和评估 v2 state，而不是立即实现 coarse transition 或
> coverage-stall。

理由是随机小实验中 v2 reduction 只有 25.75%，所有执行仍有新状态。下一轮宜先对
`canonical_node_shapes`、voting/catch-up 的字段组合做基数贡献分析，并用更多定向轨迹
验证哪些字段可以进一步归并；同时需要决定分区拓扑应来自 Trace 上下文还是另设行为
维度。只有当随机实验的 novelty 曲线出现更稳定的降速，同时定向 election、
crash/restart、snapshot 和 mutant 边界仍可区分后，再开始 coarse semantic transition
和 coverage-stall 指标。
