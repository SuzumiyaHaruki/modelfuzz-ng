# Facet v1 Final Freeze

最终状态：**PARTIAL**

## 1. 为什么是 PARTIAL

Stage 1—7 已证明 Facet v1 的机制、证据和确定性边界成立：

- Completed Execution Record可稳定构建；
- Offline Facet evaluator与三个冻结 Raft Facet可消费真实执行；
- Facet Breadth union、First/Shortest与decision确定；
- strict TLC、公平性、重复性、overlap lineage等价均成立；
- invalid/insufficient evidence为0；
- 46个representative slots全部replay/re-execution验证；
- mutant failure稳定并可minimize到18 actions。

但最终不能判定 GO：

1. 旧历史材料缺少逐槽 initial Plan identity，预注册已把结论上限冻结为
   `PARTIAL`；
2. closed-tree下facet-only更易queue exhaustion，concrete trace diversity明显更低；
3. neutral-reseed下facet-only unique TraceDigest仍略低；
4. active结果是mixed signal，而不是一致优于current baseline。

单纯性能负面不构成基础设施 `BLOCKED`，所以最终为 `PARTIAL`。

## 2. Frozen v1 能力

以下内容最终冻结：

- execution record schema：
  `modelfuzz-ng-completed-execution-v1`，major 1；
- FacetKey typed/canonical/digest规则；
- evaluation statuses：
  `evaluated`、`not_applicable`、`insufficient_evidence`、
  `invalid_evidence`；
- Catalog v1：
  - `raft.election_role_term_shape` v1，13 classes；
  - `raft.replication_alignment_shape` v1，8 classes；
  - `raft.snapshot_lifecycle_event` v1，10 classes；
- Facet Breadth schemas、eligibility、strict ordinal、atomic Apply；
- First按Apply顺序不可变；
- Shortest五字段 comparator；
- 五种 Decision reason；
- 每key First/Shortest紧凑上界。

这些定义不因Stage 6/7性能结果而修改。

## 3. 最终可支持的声明

可以声明：

1. Facet v1是只读、离线、确定性的协议语义投影；
2. 三个Raft Facet在真实etcd-raft短轨迹和正式campaign中提供非退化信号；
3. Facet Breadth可确定维护union及First/Shortest representatives；
4. representative可由真实replay、strict TLC、Oracle和Facet重算验证；
5. Facet可作为diagnostic、Assurance Matrix、Agent planning和Goal seed analysis的
   稳定语义输入；
6. neutral supply下，Facet stream观察到更广model-semantic breadth、更多rare
   snapshot behavior，并提高本次snapshot-status mutant detection success。

## 4. 明确不支持的声明

不得声明：

- facet-only active guidance普遍优于current baseline；
- Facet替代raw/semantic/model coverage；
- Facet应单独成为长期Corpus admission policy；
- closed-tree queue exhaustion已解决；
- Goal、Agent或hybrid guidance已经验证；
- 旧Facet campaign已逐candidate exact replication；
- 结果可以外推到其他协议、节点数、模型bounds或无限预算。

## 5. Active guidance 最终结论

Closed-tree结果确认Facet有限Catalog会使active parent supply较快耗尽。Neutral
reseed表明这不是Facet evidence失效：补充中立candidate supply后，Facet仍能形成完整
decision、覆盖全部snapshot catalog，并对rare behavior和mutant detection产生局部
正向信号。但concrete TraceDigest没有改善。

因此：

- Offline Facet Core v1：完成；
- Facet Breadth/representative Core v1：完成；
- facet-only长期active superiority：未成立；
- facet-only不应在当前证据下单独替代current baseline admission。

## 6. Stage 1—7 evidence chain

| Stage | 冻结/验证内容 |
|---|---|
| 1 | Completed Execution Record typed/versioned/deterministic |
| 2 | Facet evidence matrix、scope、key、三个Raft Facet contract |
| 3 | Offline Facet Core和golden/metamorphic validation |
| 4 | 真实etcd-raft pilot、31-class Catalog与Breadth contract |
| 5 | 纯内存Catalog/Summary/Coverage/First/Shortest/Decision |
| 6 | strict TLC主动A/B机制GO、性能SIGNAL_NEGATIVE |
| 7 | historical/held-out/mutant/replay/minimize，最终PARTIAL |

Stage 7正式TLC累计10,648 requests、362,972 model events，10,648 succeeded、
0 failed。Results目录只含紧凑JSON/CSV与两份minimized Plan，不含完整Trace或
Model State。

## 7. 后续模块可以依赖什么

Goal、Assurance或未来Agent proposal validator可以只读依赖：

- `CompletedExecutionRecordV1` identity/outcome摘要；
- frozen Catalog/FacetKey/Evaluation；
- candidate-level Facet summary；
- Breadth Decision和First/Shortest representative reference；
- replay-verified representative evidence；
-明确的missing/invalid evidence状态。

它们不得反向进入Runtime、Engine、Adapter、Mapper、TLC、Oracle或Corpus，也不得让
Agent在运行时自由判断class。

## 8. Facet v2 版本规则

以下任一语义变化必须创建新version，不得静默修改v1：

- class membership predicate变化；
- class set增删或重命名；
- scope、required evidence或invariance变化；
- FacetKey canonical/digest payload变化；
- Breadth eligibility、First或Shortest comparator变化。

只改变说明文字、debug explanation或测试报告不改变v1 identity，但仍需证明不进入
canonical payload。

## 9. 主线结束判断

**可以结束 Facet v1 主线。**

结束的含义是：Offline evaluator和Breadth/representative机制已冻结；主动
facet-only superiority未成立。下一阶段若进入Goal、Assurance或Agent，应把Facet作为
稳定语义输入，而不是继续为得到正向active结果调整v1 class、budget或mutator。
