# Pre-v1 实验记录索引

本目录保存正式 v1 之前的验证结论，只用于说明设计依据，不代表当前可恢复的 artifact
schema。历史文档中的 checkpoint/semantic 版本均为内部候选编号；正式 v1 不兼容这些
格式。仓库根目录的 `runs/` 已在正式 v1 基线重置时清空，文档中的运行路径仅作历史
定位，不保证仍存在。

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
| Storage/Snapshot TLA+ | `storage-snapshot-model-phase1-20260722.md`、`storage-snapshot-model-e2e-20260722.md`、`snapshot-progress-model-phase2-20260722.md`、`snapshot-install-model-phase3-20260722.md`、`snapshot-fast-forward-status-phase4-20260722.md` |
| Snapshot strict soak、吞吐与重放 | `snapshot-strict-soak-20260722.md` |
| Snapshot 与日志压缩 | `snapshot-compaction-20260721.md` |
| 网络分区、合并与五节点 smoke | `network-partition-20260722.md` |
| 定向 partition/snapshot 与失败缩减 | `directed-snapshot-minimization-20260722.md` |
| n/3+1 quorum mutant | `quorum-one-third-mutant-20260721.md` |
| LLM 接入准备 | `deepseek-readiness-20260721.md` |

## 正式 v1 之后的保留规则

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

## Pre-v1 历史清理记录

正式 v1 重置时，pre-v1 的 `runs/` 原始产物已整体移入系统回收站，仓库内从空的
`runs/` 重新开始。可复核的配置、seed、统计和结论保留在本目录的带日期报告中；报告
引用的旧运行路径不再保证存在。回收站由用户在确认不再需要恢复后单独清空。
