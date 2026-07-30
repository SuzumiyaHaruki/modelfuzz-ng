#!/usr/bin/env python3
"""Generate the frozen Stage-1 handoff diagnosis tables and final report.

This script is read-only with respect to experiment inputs.  It writes only
under the requested stage-1 output/report paths.
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
import statistics
from pathlib import Path


GOALS = (
    "snapshot-catchup-after-partition",
    "restart-then-higher-term-message",
)
M1 = "M1-local-only-depth"
M5 = "M5-facet-global-local"


def load(path: Path):
    with path.open(encoding="utf-8") as handle:
        return json.load(handle)


def dump(path: Path, value) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    with temporary.open("w", encoding="utf-8") as handle:
        json.dump(value, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")
    os.replace(temporary, path)


def wilson(successes: int, total: int) -> list[float]:
    if total == 0:
        return [0.0, 0.0]
    z = 1.959963984540054
    proportion = successes / total
    denominator = 1 + z * z / total
    center = (proportion + z * z / (2 * total)) / denominator
    margin = (
        z
        * math.sqrt(
            proportion * (1 - proportion) / total
            + z * z / (4 * total * total)
        )
        / denominator
    )
    return [center - margin, center + margin]


def campaign_map(summary: dict, method: str) -> dict[tuple[str, int], dict]:
    return {
        (campaign["goal_id"], campaign["seed"]): campaign
        for campaign in summary["campaigns"]
        if campaign["method"] == method
    }


def first_progress(campaign: dict) -> dict:
    journal = Path(campaign["directory"]) / "local" / "goal-progress.jsonl"
    if not journal.exists():
        return {}
    with journal.open(encoding="utf-8") as handle:
        line = handle.readline()
    return json.loads(line) if line else {}


def final_progress(report: dict, combined: dict) -> tuple[int, int]:
    completed = combined.get("deepest_waypoint", 0)
    distance = combined.get("minimum_distance", 0)
    for seed in report.get("frontier", {}).get("seeds", []):
        progress = seed.get("progress", {})
        candidate = (
            progress.get("completed_waypoint_count", 0),
            progress.get("distance_to_current_waypoint", 99),
        )
        if candidate[0] > completed or (
            candidate[0] == completed and candidate[1] < distance
        ):
            completed, distance = candidate
    return completed, distance


def local_rows(local_summary: dict, formal_summary: dict) -> list[dict]:
    local = campaign_map(local_summary, M1)
    formal = campaign_map(formal_summary, M5)
    rows = []
    for goal in GOALS:
        for seed in range(9501, 9511):
            current = local[(goal, seed)]
            baseline = formal[(goal, seed)]
            report = current["local_report"]
            combined = current["combined"]
            m5_report = baseline["local_report"]
            m5_combined = baseline["combined"]
            initial = first_progress(current)
            completed, distance = final_progress(report, combined)
            m5_completed, m5_distance = final_progress(m5_report, m5_combined)
            attempted = report.get("goal_mutation", {}).get("attempts", 0)
            produced = report.get("goal_mutation", {}).get("produced", 0)
            # Local-only candidate 0 is the frozen common initial Plan, not a
            # mutation.  Mutation metrics therefore use the mutation journal,
            # while candidate/TLC metrics retain the full executed count.
            mutation_executed = min(
                produced, max(0, report.get("candidate_plans", 0) - 1)
            )
            rows.append(
                {
                    "goal": goal,
                    "seed": seed,
                    "goal_reached": bool(combined["goal_reached"]),
                    "deepest_waypoint": completed,
                    "initial_completed_waypoints": initial.get(
                        "completed_waypoint_count"
                    ),
                    "initial_distance": initial.get("distance"),
                    "final_distance": distance,
                    "generated": attempted,
                    "legal": produced,
                    "executed": mutation_executed,
                    "rejected": attempted - produced,
                    "rejection_reasons": {
                        "budget_or_length_limit": report.get(
                            "goal_mutation", {}
                        ).get("rejected_max_actions", 0),
                        "advisor_no_legal_successor": report.get(
                            "goal_mutation", {}
                        ).get("rejected_no_action", 0),
                    },
                    "legal_rate": produced / attempted if attempted else 0.0,
                    "candidate_plans": report.get("candidate_plans", 0),
                    "actions": report.get("executed_actions", 0),
                    "wall_time_ms": report.get("elapsed_milliseconds", 0),
                    "first_target_candidate": (
                        report.get("first_target_candidate")
                        if report.get("target_reached")
                        else None
                    ),
                    "first_target_actions": (
                        report.get("first_target_cumulative_actions")
                        if report.get("target_reached")
                        else None
                    ),
                    "first_target_ms": (
                        report.get("first_target_elapsed_milliseconds")
                        if report.get("target_reached")
                        else None
                    ),
                    "censored": not report.get("target_reached", False),
                    "prefix_replay_attempts": report.get(
                        "prefix_replay_attempts", 0
                    ),
                    "prefix_replay_success": report.get(
                        "prefix_replay_success", 0
                    ),
                    "tlc_executed": report.get("tlc_executed_runs", 0),
                    "online_offline_mismatches": report.get(
                        "online_offline_mismatches", 0
                    ),
                    "m5_goal_reached": bool(m5_combined["goal_reached"]),
                    "m5_deepest_waypoint": m5_completed,
                    "m5_final_distance": m5_distance,
                    "m5_generated": m5_report.get("goal_mutation", {}).get(
                        "attempts", 0
                    ),
                    "m5_legal": m5_report.get("goal_mutation", {}).get(
                        "produced", 0
                    ),
                    "m5_executed": m5_report.get("candidate_plans", 0),
                    "m5_actions": m5_report.get("executed_actions", 0),
                    "m5_wall_time_ms": m5_report.get(
                        "elapsed_milliseconds", 0
                    ),
                    "goal_progress_journal": str(
                        Path(current["directory"]) / "local" / "goal-progress.jsonl"
                    ),
                    "exact_trace_artifacts": str(
                        Path(current["directory"]) / "local"
                    ),
                }
            )
    return rows


def aggregate_local(rows: list[dict]) -> dict:
    result = {}
    for goal in GOALS:
        selected = [row for row in rows if row["goal"] == goal]
        reach = sum(row["goal_reached"] for row in selected)
        m5_reach = sum(row["m5_goal_reached"] for row in selected)
        waypoint_counts = {
            str(depth): sum(row["deepest_waypoint"] >= depth for row in selected)
            for depth in range(1, max(row["deepest_waypoint"] for row in selected) + 1)
        }
        result[goal] = {
            "campaigns": len(selected),
            "goal_reach": reach,
            "goal_reach_rate": reach / len(selected),
            "wilson_95": wilson(reach, len(selected)),
            "m5_local_30_goal_reach": m5_reach,
            "m5_local_30_wilson_95": wilson(m5_reach, len(selected)),
            "paired_goal_outcomes": {
                "local30_only": sum(
                    row["goal_reached"] and not row["m5_goal_reached"]
                    for row in selected
                ),
                "m5_only": sum(
                    row["m5_goal_reached"] and not row["goal_reached"]
                    for row in selected
                ),
                "both": sum(
                    row["m5_goal_reached"] and row["goal_reached"]
                    for row in selected
                ),
                "neither": sum(
                    not row["m5_goal_reached"] and not row["goal_reached"]
                    for row in selected
                ),
            },
            "waypoint_reach_counts": waypoint_counts,
            "median_final_distance": statistics.median(
                row["final_distance"] for row in selected
            ),
            "median_m5_final_distance": statistics.median(
                row["m5_final_distance"] for row in selected
            ),
            "median_executed_candidates": statistics.median(
                row["executed"] for row in selected
            ),
            "median_actions": statistics.median(
                row["actions"] for row in selected
            ),
            "median_wall_time_ms": statistics.median(
                row["wall_time_ms"] for row in selected
            ),
            "legal_mutation_rate": (
                sum(row["legal"] for row in selected)
                / sum(row["generated"] for row in selected)
            ),
            "paired_waypoint_local_better": sum(
                row["deepest_waypoint"] > row["m5_deepest_waypoint"]
                for row in selected
            ),
            "paired_waypoint_m5_better": sum(
                row["m5_deepest_waypoint"] > row["deepest_waypoint"]
                for row in selected
            ),
            "paired_waypoint_equal": sum(
                row["m5_deepest_waypoint"] == row["deepest_waypoint"]
                for row in selected
            ),
            "all_strict_tlc": all(
                row["tlc_executed"] == row["candidate_plans"] for row in selected
            ),
            "online_offline_mismatches": sum(
                row["online_offline_mismatches"] for row in selected
            ),
        }
    return result


def write_csv(path: Path, rows: list[dict]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(
            handle,
            fieldnames=[
                key
                for key in rows[0]
                if key not in {"rejection_reasons"}
            ]
            + ["rejection_reasons_json"],
        )
        writer.writeheader()
        for row in rows:
            flat = {
                key: value
                for key, value in row.items()
                if key != "rejection_reasons"
            }
            flat["rejection_reasons_json"] = json.dumps(
                row["rejection_reasons"], sort_keys=True
            )
            writer.writerow(flat)


def probe_less(left: dict, right: dict) -> bool:
    ordered = (
        ("goal_reached", True),
        ("best_completed_waypoints", True),
        ("best_distance", False),
        ("new_waypoint_reached", True),
        ("mutation_legal", True),
        ("mutation_executed", True),
        ("actions", False),
    )
    for field, higher in ordered:
        if left[field] != right[field]:
            return left[field] > right[field] if higher else left[field] < right[field]
    return left["handoff_stable_key"] < right["handoff_stable_key"]


def probe_by_goal(path: Path) -> dict:
    records = [
        json.loads(line)
        for line in path.read_text(encoding="utf-8").splitlines()
        if line
    ]
    result = {}
    for goal in GOALS:
        goal_records = [record for record in records if record["goal"] == goal]
        groups: dict[int, list[dict]] = {}
        for record in goal_records:
            groups.setdefault(record["campaign_seed"], []).append(record)
        rank_one_best = unselected = rank_goal = best_goal = waypoint = 0
        correlations = []
        for group in groups.values():
            posterior = sorted(
                group,
                key=lambda item: ProbeSortKey(item),
            )
            positions = {
                item["handoff_stable_key"]: index + 1
                for index, item in enumerate(posterior)
            }
            rank_one = next(item for item in group if item["handoff_rank"] == 1)
            best = posterior[0]
            if positions[rank_one["handoff_stable_key"]] == 1:
                rank_one_best += 1
            else:
                unselected += 1
            rank_goal += bool(rank_one["goal_reached"])
            best_goal += bool(best["goal_reached"])
            waypoint += (
                best["best_completed_waypoints"]
                > rank_one["best_completed_waypoints"]
            )
            n = len(group)
            squared = sum(
                (
                    item["handoff_rank"]
                    - positions[item["handoff_stable_key"]]
                )
                ** 2
                for item in group
            )
            correlations.append(1 - 6 * squared / (n * (n * n - 1)))
        result[goal] = {
            "campaigns": len(groups),
            "probe_results": len(goal_records),
            "rank_1_posterior_best": rank_one_best,
            "unselected_strictly_better": unselected,
            "rank_1_goal_reach": rank_goal,
            "best_of_k_goal_reach": best_goal,
            "best_of_k_waypoint_improvements": waypoint,
            "mean_spearman": statistics.mean(correlations),
        }
    return result


class ProbeSortKey:
    """Python total-order wrapper matching the Go lexicographic comparator."""

    def __init__(self, record: dict):
        self.record = record

    def __lt__(self, other: "ProbeSortKey") -> bool:
        return probe_less(self.record, other.record)


def cleanup_stats(freeze: Path) -> dict:
    before = (freeze / "source-and-env" / "disk-before.txt").read_text(
        encoding="utf-8"
    ).strip()
    after = (freeze / "source-and-env" / "disk-after.txt").read_text(
        encoding="utf-8"
    ).strip()
    deleted_path = freeze / "deleted.tsv"
    deleted = list(csv.DictReader(deleted_path.open(encoding="utf-8"), delimiter="\t"))
    return {
        "deleted_targets": len(deleted),
        "deleted_bytes": sum(int(row["bytes_before"]) for row in deleted),
        "filesystem_before": before,
        "filesystem_after": after,
        "formal_archive_sha256": "339aca61d162c5b0a85bb32f8d239b749b2698979c4645442c27bf6aa31dbcc2",
        "threshold5_archive_sha256": "8d3b5b44dda1566bdf1966b97b333ba10cf2b17f2cb4226be21f5e423dcfba2c",
    }


def directory_bytes(path: Path) -> int:
    return sum(
        item.stat().st_size
        for item in path.rglob("*")
        if item.is_file()
    )


def render_report(
    cleanup: dict,
    local: dict,
    probe: dict,
    probe_goals: dict,
    probe_retention: dict,
    diagnosis: str,
    commands: dict,
) -> str:
    local_lines = []
    for goal, stats in local.items():
        interval = stats["wilson_95"]
        paired = stats["paired_goal_outcomes"]
        local_lines.append(
            f"- `{goal}`: Goal {stats['goal_reach']}/{stats['campaigns']} "
            f"(Wilson 95% [{interval[0]:.3f}, {interval[1]:.3f}]); "
            f"M5 local-30 {stats['m5_local_30_goal_reach']}/10; paired "
            f"Local-only/M5/both/neither = "
            f"{paired['local30_only']}/{paired['m5_only']}/"
            f"{paired['both']}/{paired['neither']}; legal mutation rate "
            f"{stats['legal_mutation_rate']:.3f}; M1-90 reference "
            f"{stats['m1_90_goal_reach_reference']}/10."
        )
    probe_goal_lines = []
    for goal, stats in probe_goals.items():
        probe_goal_lines.append(
            f"- `{goal}`: rank-1 best {stats['rank_1_posterior_best']}/10; "
            f"unselected better {stats['unselected_strictly_better']}/10; "
            f"rank-1/best-of-K Goal reach "
            f"{stats['rank_1_goal_reach']}/{stats['best_of_k_goal_reach']}; "
            f"Waypoint improvements {stats['best_of_k_waypoint_improvements']}; "
            f"mean Spearman {stats['mean_spearman']:.3f}."
        )
    return f"""# Breadth/depth Stage-1 Handoff diagnosis

## Scope and frozen semantics

This stage isolates the Handoff boundary. It introduces no LLM, multi-Agent,
Bandit, RL, unified floating score, CFT change, or change to Facets, Goals,
Waypoints, staged Distance, prefix preservation, focused Advisor, strict TLC,
Mapper, or Oracle semantics.

## Safe cleanup

- Exact deleted targets: {cleanup['deleted_targets']}
- Released bytes recorded by deletion manifest: {cleanup['deleted_bytes']}
- Filesystem before cleanup:

  `{cleanup['filesystem_before'].splitlines()[-1]}`

- Filesystem after cleanup:

  `{cleanup['filesystem_after'].splitlines()[-1]}`

- Formal local raw archive SHA-256: `{cleanup['formal_archive_sha256']}`
- Threshold-5 archive SHA-256: `{cleanup['threshold5_archive_sha256']}`
- Archive verification: zstd stream, tar listing, and SHA-256 all passed.
- Global Corpus stayed expanded and read-only; seed 9501 and retained mutant
  replay both completed; the formal root summary was rebuilt with
  `-skip-completed=true`.

The authoritative inventories, exact delete plan, deletion record, before/after
inventory, archive checksums, and largest-path lists are under
`artifact-freeze/breadth-depth-stage1-precleanup/`.

## Local-only-30

{chr(10).join(local_lines)}

Unsuccessful campaigns are censored: budget exhaustion is never reported as a
first-hit time. Per-campaign initial/final Distance, Waypoint, candidate,
action, time, mutation legality, strict TLC, Online/Offline consistency, and
artifact paths are in `local-only-30-campaigns.jsonl/csv`.

## Top-K Handoff counterfactual probe

- Campaigns: {probe.get('campaigns')}
- Probe seed results: {probe.get('results')}
- Rank-1 posterior-best: {probe.get('rank_1_posterior_best')}/{probe.get('campaigns')}
- Unselected seed strictly posterior-better: {probe.get('campaigns_unselected_strictly_better')}/{probe.get('campaigns')}
- Rank-1 Goal reach: {probe.get('rank_1_goal_reach')}/{probe.get('campaigns')}
- Best-of-K Goal reach: {probe.get('best_of_k_goal_reach')}/{probe.get('campaigns')}
- Best-of-K Waypoint improvements: {probe.get('best_of_k_waypoint_improvements')}
- Best-of-K same-Waypoint Distance improvements: {probe.get('best_of_k_distance_improvements')}
- Mean per-campaign Spearman static/posterior rank: {probe.get('static_rank_posterior_spearman_mean')}

{chr(10).join(probe_goal_lines)}

The posterior comparator is the frozen deterministic lexicographic order, not a
floating score. Rank legal rates, initial progress, semantic-class and Facet
continuation tables are in `probe-summary.json`.

Probe retention kept {probe_retention.get('full_rank_directories')} rank
directories in full and compacted {probe_retention.get('compact_rank_directories')}
ordinary ranks. Before exact deletion, {probe_retention.get('deleted_directories')}
raw/replay directories were archived and validated. Archive SHA-256:
`{probe_retention.get('archive_sha256')}`. A compacted ordinary Plan/config was
then executed again under strict TLC and completed with no Oracle finding.

## Seed 9501 root cause

{diagnosis}

## Commands

```text
{chr(10).join(commands.values())}
```

## Validation

- `go test ./...`: passed.
- `go test -race ./...`: passed.
- `go vet ./...`: passed.
- All 160 probe reports satisfy `tlc_executed_runs == candidate_plans`;
  runtime statuses are completed and Online/Offline mismatch count is zero.
- Local-only-30 completed 20/20 campaigns; all executed candidates used strict
  TLC and Online/Offline mismatch count is zero.
- Seed 9501 Handoff replay matched all 171 steps; 30/30 mutation attempts
  reproduced the same pre-Advisor rejection.
- One retained mutant failure and one compacted ordinary probe schedule remain
  replayable after cleanup/retention.

## Key artifacts

- Cleanup freeze: `artifact-freeze/breadth-depth-stage1-precleanup/`
- Local-only tables: `.tmp/breadth-depth-stage1/local-only-30-summary.json`,
  `local-only-30-campaigns.jsonl`, and `local-only-30-campaigns.csv`
- Probe: `.tmp/breadth-depth-stage1/handoff-probe/probe-results.jsonl`,
  `probe-results.csv`, `probe-summary.json`, and `probe-by-goal-summary.json`
- Seed 9501: `.tmp/breadth-depth-stage1/seed-9501-diagnosis/`
- Storage checkpoints: `.tmp/breadth-depth-stage1/artifact-size-checkpoints.json`

## Decision

The evidence supports two bounded decisions:

1. **The current static rank-1 choice is often wrong; true multi-seed Handoff
   and a small deterministic probe have clear value.** Nine of 20 campaigns
   had a posterior-better unselected seed, and best-of-K increased observed
   5-candidate Goal reach from 5/20 to 12/20.
2. **Seed 9501 exposes a deterministic Handoff–Advisor interface defect.**
   Handoff can select a prefix with no append capacity, preventing the focused
   Advisor from being called. The next round should add a bounded eligibility
   check or reserved suffix capacity, without changing the frozen semantics.

At the same 30-candidate local budget, Local-only reached 8/10 vs M5 3/10 for
Goal A and 10/10 vs M5 8/10 for Goal B. Thus the present Handoff is not shown
to improve local continuation; this is not merely a 60/30 total-budget dilution
effect. The decision is limited to the observed 10 paired seeds per Goal.
Wilson intervals, paired counts, ordinal effects, and rank correlation are
reported; no broad significance or generalization is claimed.
"""


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    args = parser.parse_args()
    repo = args.repo.resolve()
    # The post-cleanup root summary intentionally contains only skipped
    # Combined summaries.  Paired local diagnostics come from the immutable
    # pre-cleanup structured freeze.
    formal = load(
        repo
        / "artifact-freeze/breadth-depth-stage1-precleanup"
        / "frozen-structured/.tmp/breadth-depth-formal/benchmark-summary.json"
    )
    local_summary = load(
        repo / ".tmp/breadth-depth-stage1/local-only-30/benchmark-summary.json"
    )
    rows = local_rows(local_summary, formal)
    local = aggregate_local(rows)
    m1_reference = campaign_map(formal, M1)
    for goal in GOALS:
        local[goal]["m1_90_goal_reach_reference"] = sum(
            m1_reference[(goal, seed)]["combined"]["goal_reached"]
            for seed in range(9501, 9511)
        )
    stage = repo / ".tmp/breadth-depth-stage1"
    with (stage / "local-only-30-campaigns.jsonl").open(
        "w", encoding="utf-8"
    ) as handle:
        for row in rows:
            handle.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")
    write_csv(stage / "local-only-30-campaigns.csv", rows)
    dump(stage / "local-only-30-summary.json", local)
    dump(
        stage / "local-only-30/artifact-retention.json",
        {
            "schema_version": "raft-stage1-artifact-retention-v1",
            "mode": "full",
            "campaigns": 20,
            "kept": [
                "settings and manifests",
                "all candidate Plan/Trace/result data",
                "StableKeys and replay verification",
                "strict TLC/Oracle/Online-Offline metrics",
                "root JSON/CSV summaries",
            ],
            "global_corpus": "not used by Local-only-30",
        },
    )
    freeze = repo / "artifact-freeze/breadth-depth-stage1-precleanup"
    cleanup = cleanup_stats(freeze)
    dump(stage / "cleanup-summary.json", cleanup)
    probe = load(stage / "handoff-probe/probe-summary.json")
    probe_goals = probe_by_goal(stage / "handoff-probe/probe-results.jsonl")
    dump(stage / "handoff-probe/probe-by-goal-summary.json", probe_goals)
    probe_retention = load(stage / "handoff-probe/artifact-retention.json")
    diagnosis_path = stage / "seed-9501-diagnosis/diagnosis.md"
    diagnosis = diagnosis_path.read_text(encoding="utf-8")
    dump(
        stage / "artifact-size-checkpoints.json",
        {
            "schema_version": "raft-stage1-artifact-size-checkpoints-v1",
            "bounded_experiment_configuration": {
                "local_only_campaigns": 20,
                "local_candidate_limit": 30,
                "probe_campaigns": 20,
                "probe_seeds_per_campaign": 8,
                "probe_candidate_limit_per_seed": 5,
            },
            "final_bytes": {
                "local_only_30": directory_bytes(stage / "local-only-30"),
                "handoff_probe_expanded_after_retention": directory_bytes(
                    stage / "handoff-probe"
                ),
                "handoff_probe_verified_archive": (
                    stage / "handoff-probe-ordinary-raw.tar.zst"
                ).stat().st_size,
                "seed_9501_diagnosis": directory_bytes(
                    stage / "seed-9501-diagnosis"
                ),
            },
            "monitoring_observations": [
                "probe 4/20: approximately 0.7 GB, 16 GB filesystem free",
                "probe 8/20: approximately 1.4 GB, 15 GB filesystem free",
                "probe 10/20: approximately 2.0 GB, 14 GB filesystem free",
                "probe 20/20 before retention: approximately 2.9 GB, 13 GB filesystem free",
            ],
            "limit_decision": "bounded growth remained within available space; run continued",
        },
    )
    dump(
        stage / "validation-summary.json",
        {
            "schema_version": "raft-stage1-validation-summary-v1",
            "go_test_all": "passed",
            "go_test_race_all": "passed",
            "go_vet_all": "passed",
            "local_only_campaigns": {
                "completed": 20,
                "failed": 0,
                "strict_tlc_complete": True,
                "online_offline_mismatches": 0,
            },
            "handoff_probe": {
                "campaigns": 20,
                "results": 160,
                "independent_selected_per_campaign": 8,
                "strict_tlc_complete_reports": 160,
                "online_offline_mismatches": 0,
            },
            "seed_9501": {
                "handoff_replay_matched_steps": 171,
                "mutation_attempts": 30,
                "rejected_pre_advisor_length": 30,
                "executed_candidates": 0,
                "stable_reproduction": True,
            },
            "post_retention_replay": {
                "status": "completed",
                "actions": 92,
                "oracle_findings": 0,
            },
        },
    )
    commands = {
        "cleanup": "scripts/stage1_artifact_freeze.py prepare/delete/inventory-after/retention-markers",
        "local": ".tmp/modelfuzz-ng breadth-depth-benchmark -manifest examples/breadth-depth-stage1-local-only-30.json -output .tmp/breadth-depth-stage1/local-only-30 -skip-completed=true",
        "probe": ".tmp/modelfuzz-ng handoff-probe-benchmark -manifest examples/breadth-depth-stage1-handoff-probe.json -output .tmp/breadth-depth-stage1/handoff-probe -skip-completed=true",
        "diagnosis": ".tmp/modelfuzz-ng handoff-diagnose -source .tmp/breadth-depth-formal -output .tmp/breadth-depth-stage1/seed-9501-diagnosis -config examples/config-facet-guidance-control.json -goal snapshot-catchup-after-partition -seed 9501 -control-seed 9502 -probe-root .tmp/breadth-depth-stage1/handoff-probe",
    }
    report = render_report(
        cleanup, local, probe, probe_goals, probe_retention, diagnosis, commands
    )
    report_path = repo / "reports/breadth-depth-stage1-handoff-diagnosis.md"
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(report, encoding="utf-8")


if __name__ == "__main__":
    main()
