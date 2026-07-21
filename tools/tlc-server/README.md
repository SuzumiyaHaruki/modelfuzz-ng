# ModelFuzz-NG controlled TLC Server

该子项目是 NG 自己维护的严格受控 TLC 服务。它使用官方 TLA+ Tools，不再要求
克隆原 ModelFuzz artifact。构建脚本固定下载 TLA+ Tools `v1.8.0`，并校验
SHA-256；下载文件和构建产物不会进入 Git。

启动 Raft 模型：

```bash
tools/tlc-server/run.sh \
  --model models/raft/raft.tla \
  --config models/raft/raft.cfg \
  --port 2024
```

服务提供：

- `GET /health`：返回服务版本、模型路径和严格模式；
- `POST /execute`：兼容当前 Go TLC Client 的事件数组和成功响应格式；
- 对映射失败、disabled action、多个后继状态、非法状态和 invariant 违反返回
  带稳定 `code`、事件序号和事件名的 JSON 错误。
- 每个新状态完整检查 model constraint 和 invariant；重复状态使用最多10万个条目的
  有界 fingerprint 验证缓存，避免反馈实验反复计算同一状态。

当前服务只实现 NG Raft 模型使用的事件协议，且每次请求都从唯一初始状态开始。
这是有意的边界：跨请求共享模型状态和协议无关 Java Mapper 暂不进入第一版。

运行服务端集成测试：

```bash
tools/tlc-server/test.sh
```

本目录实现参考了 ModelFuzz controlled TLC Server 的事件协议。ModelFuzz artifact
及 TLA+ Tools 均采用 MIT License，具体归属见 `THIRD_PARTY_NOTICES.md`。
