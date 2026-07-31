# Old / New Facet Compatibility

状态：在 held-out 运行前冻结的人工语义兼容表。

旧 schema 为 `raft-coverage-facets-v1-prototype`，新 schema 由三个有限 Catalog
Facet 构成。下表比较概念，不比较 digest。

| old concept / evidence | old class family | new facet / class | relation | 可作 historical comparison | 说明 |
|---|---|---|---|---|---|
| active role topology、leader/candidate 数、active term topology | Election 组合 key | `raft.election_role_term_shape` 的 12 个 leader/candidate/term 组合与 `no_running_nodes` | new narrower | 是，方向性 | 新 Facet只保留三轴有限组合，丢弃 quorum、vote boundary、votedFor 与 crashed bucket |
| stable/no leader | Election leader mode | `leaders_one_*` / `leaders_none_*` | old narrower | 是，聚合后 | 旧 value还组合 candidate vote/quorum；只能按 leader mode 聚合 |
| same/split term | Election term topology | `*_terms_uniform` / `*_terms_split` | exact | 是 | 都忽略 uniform term shift；新 key同时冻结 leader/candidate population |
| multiple leaders | Election leader count | `leaders_multiple_*` | exact | 是 | 需按 candidate 与 term 子类聚合 |
| log topology | Replication log relation | `log_aligned_*` / `log_diverged_*` | new narrower | 是，方向性 | 新 Facet只问跨节点 last index 是否相等，不保留 prefix/conflict 类型 |
| commit lag bucket multiset | Replication commit lag | `*_commit_aligned_*` / `*_commit_diverged_*` | new narrower | 是，方向性 | 新 Facet不保留 zero/one/small/large |
| applied lag / catch-up topology | Replication applied/catch-up | `*_applied_aligned` / `*_applied_diverged` | new narrower | 部分 | 新 Facet不区分 append catch-up、snapshot-required 或 progress |
| committed-prefix conflict | Replication safety relation | 无等价 class | no equivalent | 否 | 新 v1 Alignment 不编码 entry value/term conflict；仍由模型/Oracle负责 |
| snapshot mode no/available/required/pending | Snapshot state mode | 无等价 class | no equivalent | 否 | 新 Snapshot Facet是 transition marker，不分类持续 state mode |
| snapshot outcome created | Snapshot outcome | `snapshot_created` | exact | 是 |
| compacted storage | Snapshot/storage outcome | `log_compacted` | exact | 是 |
| pending/send | Snapshot pending/outcome | `snapshot_sent` | new narrower | 是 | 新 class要求真实 Adapter sent marker 与 progress 边界 |
| delivered | Snapshot outcome | `snapshot_delivered` | exact | 是 |
| installed | Snapshot outcome | `snapshot_applied` | exact | 是 |
| fast-forward | Snapshot outcome | `snapshot_fast_forwarded` | exact | 是 |
| rejected/stale | Snapshot outcome | `snapshot_rejected_or_stale` | exact | 是 |
| failed / retry pending / retry succeeded | Snapshot multi-step outcome | `snapshot_status_failed` 仅覆盖单次 status | old narrower | 部分 | retry path 跨多个 step，属于 Goal/history，不进入 Facet v1 |
| status success | Snapshot status | `snapshot_status_succeeded` | exact | 是 |
| ignored status | Snapshot status | `snapshot_status_ignored` | exact | 是 |
| Recovery phase normal/crashed/restarted/caught-up | Recovery prefix state machine | 无新 Facet | belongs to Goal/history | 否 | 新 Catalog故意不保留 sticky history |
| recovery message stale/same/higher term | Recovery transition history | 无新 Facet | belongs to Goal/history | 否 | 后续 Goal/Waypoint 输入 |
| partition topology、connected quorum、leader position | Network context | 无新 Facet | no equivalent | 否 | 当前完成 evidence不保留 v1 所需完整 queue/network context |
| healed + delayed message | Network prefix history | 无新 Facet | belongs to Goal/history | 否 | 多步 sticky evidence，不是 state/transition Facet v1 |

## 结论

- 可比较的是 election/replication 的聚合方向，以及七个直接 Snapshot marker 和三种
  status class。
- Recovery、Network 和 retry path 不应强行映射到新 Catalog。
- 旧 key 包含大量组合字段，新 key 固定 31 个 class；旧 distinct 数与新 covered
  count 没有相同分母。
- Historical paired evaluation 评价当前 active admission 方向，不声称重放了旧 key
  identity 或旧 candidate tree。
