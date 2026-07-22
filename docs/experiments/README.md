# 实验记录索引与原始产物保留规则

`docs/experiments` 保存可长期阅读的实验结论；仓库根目录的 `runs/` 保存可重复生成、
不进入 Git 的原始运行产物。文档是长期记录，`runs/` 不是归档系统。历史文档中出现的
运行路径可能已经按本页规则清理。

## 当前主题索引

| 主题 | 记录 |
|---|---|
| 基础执行与模型映射 | `basic-raft-20260720.md`、`raft-oracle-20260720.md` |
| panic、前缀与重放 | `panic-prefix-20260720.md` |
| 随机基线 | `random-baseline-20260720.md` |
| crash/restart 与 timer | `crash-restart-20260721.md`、`nonleader-timer-reasons-20260721.md` |
| 反馈闭环与新颖性 | `feedback-loop-20260721.md`、`novelty-reseed-20260721.md` |
| 持久化与 checkpoint 演进 | `persistence-metrics-20260721.md`、`failure-checkpoint-v3-20260721.md`、`checkpoint-v5-tlc-metrics-20260721.md`、`checkpoint-v6-feedback-20260721.md` |
| 反馈准入与动作分布 | `feedback-tuning-v7-20260722.md` |
| 严格/按需 TLC | `strict-tlc-migration-20260721.md`、`lazy-tlc-actions-20260721.md` |
| Snapshot 与日志压缩 | `snapshot-compaction-20260721.md` |
| n/3+1 quorum mutant | `quorum-one-third-mutant-20260721.md` |
| LLM 接入准备 | `deepseek-readiness-20260721.md` |

## 保留规则

优先保留以下原始产物：

- 当前功能的最小端到端成功与失败样本；
- 相同 seed/config 的关键对照；
- checkpoint 中断恢复与未中断 control；
- 每种独立失败类别的一条完整 Trace；
- 最新格式、仍用于回归的数据。

可以在结论写入文档后清理：

- 已被新版格式替代的 checkpoint；
- 重复 replay、warm-cache 和修复过程中的中间输出；
- 已有紧凑报告的长时间 soak 原始 Corpus/Trace；
- 同一 Plan、seed 和配置产生的重复副本；
- 不再兼容当前代码且没有专门重放价值的旧运行。

清理前先检查实验报告、配置、seed、失败信息和统计口径已经进入对应 Markdown。只操作
明确列出的 `runs/<name>`，不要递归删除整个 `runs/`。优先移入系统回收站，确认无需恢复
后再由用户单独清空回收站。

## 2026-07-21 清理后的代表性数据

本次清理将 `runs/` 从约 2.5 GB 精简到约 81 MB。保留集合包括：

- checkpoint v6 的无 TLC smoke、100-seed、恢复/control 和 lazy-TLC 对照；
- snapshot 的 TLC feedback、恢复/control 和三条代表性固定 Plan；
- quorum mutant 的固定 TLC 最短反例、100-seed 正常/异常对照、seed 470724 成对完整
  Trace、seed 470729 snapshot panic，以及 seed 470723 转发修复后的 TLC 回归。

2026-07-20 的原始运行、checkpoint v5/两小时 v5 soak、TLC 迁移过程的重复运行和其他
中间副本已移入系统回收站。相关结论仍保留在本目录文档中。
