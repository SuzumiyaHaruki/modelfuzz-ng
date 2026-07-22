# TLC Action 按需绑定实验（2026-07-21）

## 问题

严格 TLC 服务原先在 `FastTool` 启动时展开完整 `Next`，再为所有参数化
Action 建立索引。5/5边界会产生434301个 Action，进程 RSS 约784MiB；
10/10中单是 `HandleAppendEntriesRequest` 的参数组合就从419904增长到
8696754，默认约2.94GiB JVM heap 在服务启动期 OOM。

## 修改

- `raft-5.cfg` 和 `raft-10.cfg` 使用不展开参数空间的 `ControlledNext`。完整
  `Next` 及所有动作操作符仍保留在模型中；`raft.cfg` 仍使用完整 `Spec`。
- `RaftEventMapper` 启动时只读取11个 `OpDefNode`。收到事件后，它按操作符
  形参顺序构造 TLC `Context` 和 `Action`，不再全量枚举。
- 具体 Action 进入默认16384条的有界 LRU 缓存，可用
  `--action-cache-size` 调整。健康和指标端点会报告缓存水位、命中、未命中、
  创建和淘汰数。
- 参数仍在创建前按 cfg 中的 Server、LargestTerm、MaxLogIndex、
  MaxValue 和 Nil 校验，边界外事件仍返回 `unmapped_action`。

## 验证

Java 集成测试使用1/1小边界同时创建两个 Tool：一个从完整 `Next`
枚举 Action，一个使用按需绑定。对选举、投票请求/响应、成为 Leader 和
追加 no-op 的每一步，两者后继状态完全一致。测试同时将缓存上限设为1，
确认连续三个事件后只保留1个 Action并发生2次 LRU 淘汰。

10/10服务在本机启动后：

```text
RSS=83712KiB
action_definitions=11 cached_actions=0 cache_limit=16384
```

对同一 Timeout 请求执行两次，只创建1个 Action，第二次命中缓存。
之后用 `seed=470721`、10/10边界运行100条严格 TLC 反馈实验：

```text
runs/tlc-ng-v6-lazy-10-seed470721-100-20260721
succeeded=100 failed=0 actions=2647 model_events=2202
unique_model_states=367 corpus=51 peak_ready_candidates=10/16
timeout=158 (5.97%) proposal_dropped=57 runtime_error=0
checkpoint.json=75416 bytes corpus.jsonl=111721 bytes
```

TLC 服务统计为2202次 Action 查询、145次创建、2057次缓存命中、0次淘汰，
命中率93.42%。Action 查询累计约47.2ms，平均约21.5µs/事件；按需创建引入的
延迟仅发生在首次出现的参数组合，未在本次实验中形成瓶颈。
