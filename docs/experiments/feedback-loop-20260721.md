# Corpus、Mutation 与反馈闭环实验

日期：2026-07-21

## 实现范围

本轮把 `experiment` 从多次彼此独立的随机运行改为反馈闭环：执行候选 Plan，
用 controlled TLC 返回的全局新 `State.Key` 决定是否保留到 Corpus，再异步生成
变异后代并放回候选队列。默认初始化仍是基于实时 Observation 的随机策略，默认
变异是本地随机结构变异；`-llm-init` 和 `-llm-mutate` 可以分别切换为 LLM。

Corpus 条目保存实际执行的 Plan、紧凑 Concrete ActionSequence、全部状态键和本次
新增状态键；完整 Trace 按逐运行产物策略单独保存，不在 Corpus/checkpoint 中重复。
相同模型
状态只会触发一次全局保留。随机 Mutation 包括动作交换、字段扰动、删除、复制、插入，以及用
成对的 `crash/restart` 包围一段已有动作，并在入队前重新进行 Plan、生命周期和动作级去重校验。

LLM 传输层已改为通用 provider 配置，支持 DeepSeek、GLM、Qwen 和 Kimi。
CLI 统一使用 `-llm-provider`、`-llm-model`、`-llm-base-url`、`-llm-api-key-env`
和 `-llm-timeout`；默认 provider 仍为 DeepSeek。

首版 Mutation 修改 Corpus 条目中的高层 Plan，再执行产生一条新的 Concrete Trace；
不直接改写旧 Trace 中已经绑定的 MessageID 和绝对时间，否则这些具体标识在新
Runtime 中通常无效。因而这里的 “Trace/Plan Mutation” 是以有价值 Trace 作为反馈
依据、以其对应 Plan 作为可执行变异载体。

## 验证项目

代码测试覆盖以下边界：

- 重复模型状态不重复进入 Corpus，输入与快照之间没有可变别名；
- 相同 seed 的本地 Mutation 完全确定，且候选均有效并区别于父 Plan；
- 反馈 Runner 会执行变异后代，变异失败后能继续补充新种子；
- 四类 provider 请求都使用 JSON Output 和 Bearer 环境变量 Key；
- LLM 初始化使用思考模式，LLM 变异使用非思考模式；
- LLM 调用次数、失败、累计时延和 token 会写入 `llm-stats.json`，按 initial/mutation
  分组，随 checkpoint 更新并在恢复后累计，且不含 Key；
- 初始化/变异 LLM 开关可同时开启，本地模拟 API 与模拟 TLC 能跑通完整闭环；
- 不连接 TLC 时继续执行随机基线，但 Corpus 明确为空。

本文件不记录任何真实 API Key。LLM 相关测试使用本地 `httptest` 服务及无效
占位字符串，不会访问外部服务或产生费用。

## 实际运行

无 TLC 随机基线：

```bash
go run ./cmd/modelfuzz-ng experiment \
  -config examples/config.json \
  -output /tmp/modelfuzz-ng-feedback/run \
  -runs 8 -max-plan-actions 12 -parallelism 2 -seed 8100
```

结果为 `8/8` 完成、96 个 Action、79 个模型事件。由于没有模型状态，Corpus 和
Mutation 均为 0，CLI 输出了预期警告。

连接原 ModelFuzz controlled TLC 后执行：

```bash
go run ./cmd/modelfuzz-ng experiment \
  -config examples/config.json \
  -tlc http://127.0.0.1:2023 \
  -output /tmp/modelfuzz-ng-tlc-feedback/run \
  -runs 12 -max-plan-actions 15 -parallelism 1 \
  -initial-population 2 -mutations-per-state 1 \
  -max-mutations-per-corpus 3 -seed 9100
```

结果覆盖 35 个唯一模型状态，保留 6 条 Corpus 轨迹，生成 14 个变异并在总预算内
执行其中 10 个；12 次执行中 10 次完成。该历史实验中另 2 次变异改变了选举前缀，
使后续 `request` 指向非 leader，当时被统计为 `unsupported_by_model`。当前实现已将
这类状态相关动作细分为 `inapplicable` no-op；有限模型的 term/log 上界则作为
`model_bound_reached` 正常结束前缀，二者都不再污染真实失败统计。
