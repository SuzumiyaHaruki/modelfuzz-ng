# DeepSeek 接入前审计（2026-07-21）

## 结论

当前代码已经可以进行小规模 DeepSeek 初始化烟雾实验。本轮没有使用真实 API Key，
也没有向外部模型发送请求；端到端验证使用本地模拟 Chat Completions 服务。

根据 DeepSeek 当前官方文档，仓库默认的 `https://api.deepseek.com`、
`deepseek-v4-flash`、`/chat/completions`、`thinking.type` 和
`response_format=json_object` 均与当前接口一致：

- <https://api-docs.deepseek.com/>
- <https://api-docs.deepseek.com/guides/thinking_mode/>
- <https://api-docs.deepseek.com/api/create-chat-completion/>

## 本轮修复

- 截断或异常 finish reason 的完成即使返回错误，也会累计已报告的 token；
- LLM 统计增加 `by_purpose.initial` 和 `by_purpose.mutation`；
- `llm-stats.json` 随 checkpoint 更新，恢复后累计，不再被新 Client 的零值覆盖；
- Prompt 和校验增加 term、log index 上限及 leader request 约束；
- 初始化必须一次返回请求数量的合法、去重 Plan，避免静默补种子造成意外调用；
- 全部 Plan 均无效或初始化数量不足时，错误会带出最多四项拒绝原因。

## 验证

以下检查通过：

```bash
go mod tidy -diff
go test ./...
go test -race ./...
go vet ./...
```

关键 LLM/反馈/CLI 包使用 shuffle 连续执行20次通过。模拟 DeepSeek CLI 烟雾实验
完成1次 initial 调用和2条真实 Raft 轨迹，2条均成功且 Plan/Trace 均唯一；模拟
usage 为150 token，最终文件的总计和 `by_purpose.initial` 都准确记录150。

## 尚存限制

- 静态 Plan 仍不能保证执行时的节点角色和消息队列与意图一致，特别是变异后
  request 可能不再面向 leader；Runtime/模型会将其记录为无效候选；
- 客户端目前不自动重试429、5xx或网络中断，避免在响应状态不明时隐式重复付费；
- Mutation 只有一个生成 worker。它能与轨迹执行并行，但高延迟模型仍可能成为瓶颈；
- controlled TLC 目前要求 `parallelism=1`，首次实验不代表大规模吞吐表现；
- 极端周期种子参数可能消耗大部分剩余运行预算，虽不会清空 mutation 队列，仍应
  使用远大于注入数量的 interval。

因此第一阶段应只开启 `-llm-init`、运行2条轨迹；检查 Plan 有效率和 token 后，
再连接 TLC 并以 `mutations-per-state=1` 开启小规模 LLM Mutation。
