# ModelFuzz-NG controlled TLC Server

该子项目是 NG 自己维护的严格受控 TLC 服务。它使用官方 TLA+ Tools，不再要求
克隆原 ModelFuzz artifact。构建脚本固定下载 TLA+ Tools `v1.8.0`，并校验
SHA-256；下载文件和构建产物不会进入 Git。

启动 Raft 模型：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft.tla \
  --config models/raft/raft-5.cfg \
  --port 2024
```

服务提供：

- `GET /health`：返回服务版本、模型/config 路径、严格模式以及 cfg 中实际的
  `largest_term`/`max_log_index`；Go CLI 会据此拒绝模型边界漂移；
- `GET /metrics`：返回请求、事件、稳定错误码以及 Action 查询、后继计算、状态校验、
  状态序列化的累计纳秒数；
- `POST /execute`：兼容当前 Go TLC Client 的事件数组和成功响应格式；
- 启动时只加载 TLA+ 动作操作符定义；收到事件后才按形参顺序绑定具体
  `Action`，并放入默认16384条的有界 LRU 缓存。可用 `--action-cache-size`
  调整，`/health` 和 `/metrics` 会暴露定义数、缓存水位、命中、未命中和淘汰数；
- 对映射失败、disabled action、多个后继状态、非法状态和 invariant 违反返回
  带稳定 `code`、事件序号和事件名的 JSON 错误。
- 每个新状态完整检查 model constraint 和 invariant；重复状态使用最多10万个条目的
  有界 fingerprint 验证缓存，避免反馈实验反复计算同一状态。

当前服务只实现 NG Raft 模型使用的事件协议，且每次请求都从唯一初始状态开始。
这是有意的边界：跨请求共享模型状态和协议无关 Java Mapper 暂不进入第一版。

`models/raft/raft-5.cfg` 是快速测试配置；`raft-10.cfg` 与原 ModelFuzz 主实验的
10/10 边界一致。这两个 controlled server 配置使用 `ControlledNext`，避免
TLC Tool 在启动时展开全部参数笛卡尔积；具体动作由服务层按事件创建。
兼容文件 `raft.cfg` 仍是5/5且保留完整 `Spec`/`Next`，可用于普通 TLC 枚举。

运行服务端集成测试：

```bash
tools/tlc-server/test.sh
```

本目录实现参考了 ModelFuzz controlled TLC Server 的事件协议。ModelFuzz artifact
及 TLA+ Tools 均采用 MIT License，具体归属见 `THIRD_PARTY_NOTICES.md`。
