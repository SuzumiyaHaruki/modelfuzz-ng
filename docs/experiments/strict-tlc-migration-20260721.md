# 严格 controlled TLC 迁移实验（2026-07-21）

## 迁移内容

- 在 `tools/tlc-server` 中实现 NG 自己维护的 controlled TLC 服务，构建时固定下载并
  校验官方 TLA+ Tools v1.8.0，不再依赖原 ModelFuzz 的 Java 源码树。
- 保持 Go 侧已有 `POST /execute` 成功响应兼容，同时增加健康检查和结构化错误。
- 严格区分无法映射、disabled action、多个后继、不完整状态、model constraint 和
  invariant 违反；每个请求必须以 reset 结束并从唯一初始状态开始。
- 补强 Raft `TypeOK`，新增 `CommittedPrefixAgreement` 和 `LogMatching` invariant。
- TLC Client 保留服务端稳定错误码。checkpoint 升级为 v4，因为官方 v1.8.0 与旧
  ModelFuzz TLC fork 对相同状态生成的 fingerprint 不同，旧 coverage 不能直接续跑。

## 严格语义发现的问题

首次运行100条反馈轨迹时有4条以 `disabled_action/Timeout` 失败。原因是对当前 Leader
强制触发 election timer 时，etcd-raft 会保持 term 和角色不变；Adapter 正确记录了
timer 尝试，但旧 Mapper 仍生成 `Timeout`。旧服务会静默跳过 disabled action，所以
此前没有暴露该偏差。

修复后，Mapper 通过动作前后的 term 判断结果：term 不变明确映射为模型 stutter，
只把恰好增加一个 term 的选举超时映射为 `Timeout`，其他跳变明确报不支持。对一次
`AdvanceTime` 内发生的多次自然超时，Mapper 使用每个 timer Effect 自带的
`term_before`/`term_after`，而不是错误地使用整个动作的总 term 跨度。

## 验证结果

- 服务端集成测试覆盖有效轨迹、disabled action、越界参数、invariant 违反和多个
  后继状态，全部通过。
- 仓库内11条选举、复制、commit、停止/恢复和重选举 Plan 全部成功，Oracle finding
  均为0。
- 相同 seed 的100条反馈实验修复前为96成功、4模型失败；修复后为100/100成功，
  覆盖327个唯一模型状态，保留47条 Corpus，执行2674个 Action。
- Go 单元测试、race test、vet 及服务端集成测试全部通过。

## 性能结论

在本机使用相同100条、相同 seed 配置时，旧 ModelFuzz 服务约用6.9秒，新严格服务
最初约用46.2秒。预热10万个状态的 invariant 验证缓存后耗时基本不变，只能排除
invariant 检查是主要瓶颈。后续 JFR profile 发现服务加载了434301个有界 Action，
而 Mapper 每处理一个事件都会线性扫描同名 Action，并反复调用 `getParameters()`；
采样热点集中在 `Context.lookup`、`Action.getParameters` 和 `parametersMatch`，真正的
`getNextStates` 只占极少数样本。

改为启动时建立完整“动作名＋有序参数元组”哈希索引后：

- 434301个 Action 对应434301个唯一键，重复键为0；
- 相同21事件请求连续运行20次从21.06秒下降到0.92秒；
- 相同 `seed=470721` 的100条反馈实验从46.056秒下降到6.732秒，吞吐从
  2.17提高到14.85 traces/s、从58.06提高到397.21 actions/s，整体提升6.84倍；
- 两次实验均为100/100成功、2674个 Action、2197个模型事件、327个唯一模型状态、
  47条 Corpus 和68条唯一状态路径；排除计时字段后，逐运行摘要和 Corpus 文件完全一致。
