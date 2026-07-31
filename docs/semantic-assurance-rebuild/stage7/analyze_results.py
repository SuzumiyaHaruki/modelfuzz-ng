#!/usr/bin/env python3
"""Analyze Stage 7 paired campaign results with the preregistered bootstrap."""

from __future__ import annotations

import csv
import hashlib
import json
import os
import random
import statistics
import sys
import tempfile
from pathlib import Path

BOOTSTRAP_SEED = 7070707
RESAMPLES = 10_000
CAMPAIGN_SCHEMA = "modelfuzz-ng-stage7-campaign-result-v1"
PREREGISTRATION_SHA = (
    "62a386a2017e4f5bf7e8164e0478b73d7a2506914db9ffeb1701a4be1ffdbf9c"
)
SEED_LIST_SHA = "00da18df2172eada4938315b3db2e1abf3b7413ba6a58a1a6c03d2d5cc4be6f6"
MODES = ("current-baseline", "facet-only")
RARE_CLASSES = (
    "snapshot_fast_forwarded",
    "snapshot_rejected_or_stale",
    "snapshot_status_ignored",
)


def load_campaigns(directory: Path) -> dict[tuple[str, int], dict[str, dict]]:
    campaigns: dict[tuple[str, int], dict[str, dict]] = {}
    for path in sorted(directory.glob("*.json")):
        with path.open("r", encoding="utf-8") as stream:
            value = json.load(stream)
        if value.get("schema") != CAMPAIGN_SCHEMA:
            continue
        if value.get("preregistration_sha256") != PREREGISTRATION_SHA:
            raise ValueError(f"{path}: preregistration SHA mismatch")
        if value.get("heldout_seed_list_sha256") != SEED_LIST_SHA:
            raise ValueError(f"{path}: held-out seed-list SHA mismatch")
        mode = value.get("mode")
        if mode not in MODES:
            raise ValueError(f"{path}: unexpected mode {mode!r}")
        key = (value["block"], int(value["seed"]))
        modes = campaigns.setdefault(key, {})
        if mode in modes:
            raise ValueError(f"{path}: duplicate campaign {key}/{mode}")
        modes[mode] = value
    for key, modes in campaigns.items():
        if set(modes) != set(MODES):
            raise ValueError(f"incomplete pair {key}: {sorted(modes)}")
    if not campaigns:
        raise ValueError(f"no {CAMPAIGN_SCHEMA} files in {directory}")
    return campaigns


def rare_reached(campaign: dict) -> int:
    values = campaign["rare_snapshot_classes"]
    return sum(bool(values[class_id]["reached"]) for class_id in RARE_CLASSES)


def trace_ratio(campaign: dict) -> float:
    metrics = campaign["metrics"]
    executed = int(metrics["executed_candidates"])
    return float(metrics["unique_trace_digests"]) / executed if executed else 0.0


def metric_value(campaign: dict, metric: str) -> float:
    values = {
        "executed_candidates": campaign["metrics"]["executed_candidates"],
        "unique_trace_digests": campaign["metrics"]["unique_trace_digests"],
        "unique_trace_ratio": trace_ratio(campaign),
        "rare_classes_reached": rare_reached(campaign),
        "raw_model_states": campaign["metrics"]["raw_model_states"],
        "semantic_states": campaign["metrics"]["semantic_states"],
        "semantic_transitions": campaign["metrics"]["semantic_transitions"],
        "unique_model_state_paths": campaign["metrics"][
            "unique_model_state_path_digests"
        ],
        "failure_detected": int(campaign["first_failure"]["detected"]),
        "failure_candidate_ordinal": campaign["first_failure"]["candidate_ordinal"],
    }
    return float(values[metric])


def percentile(sorted_values: list[float], probability: float) -> float:
    if not sorted_values:
        return 0.0
    position = probability * (len(sorted_values) - 1)
    lower = int(position)
    upper = min(lower + 1, len(sorted_values) - 1)
    fraction = position - lower
    return sorted_values[lower] * (1.0 - fraction) + sorted_values[upper] * fraction


def paired_bootstrap(
    differences: list[float],
    *,
    seed_material: str,
    higher_is_better: bool,
) -> dict:
    if not differences:
        return {
            "pairs": 0,
            "mean_difference": 0.0,
            "median_difference": 0.0,
            "percentile_95_interval": [0.0, 0.0],
            "facet_better_pairs": 0,
            "equal_pairs": 0,
            "facet_worse_pairs": 0,
        }
    digest = hashlib.sha256(seed_material.encode("utf-8")).digest()
    derived = BOOTSTRAP_SEED ^ int.from_bytes(digest[:8], "big")
    generator = random.Random(derived)
    means = []
    count = len(differences)
    for _ in range(RESAMPLES):
        means.append(
            sum(differences[generator.randrange(count)] for _ in range(count)) / count
        )
    means.sort()
    if higher_is_better:
        better = sum(value > 0 for value in differences)
        worse = sum(value < 0 for value in differences)
    else:
        better = sum(value < 0 for value in differences)
        worse = sum(value > 0 for value in differences)
    return {
        "pairs": count,
        "mean_difference": statistics.fmean(differences),
        "median_difference": statistics.median(differences),
        "percentile_95_interval": [
            percentile(means, 0.025),
            percentile(means, 0.975),
        ],
        "facet_better_pairs": better,
        "equal_pairs": sum(value == 0 for value in differences),
        "facet_worse_pairs": worse,
    }


def atomic_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".stage7-analysis-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def atomic_csv(path: Path, rows: list[dict], fields: list[str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".stage7-analysis-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="") as stream:
            writer = csv.DictWriter(stream, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def analyze(directory: Path) -> dict:
    campaigns = load_campaigns(directory)
    metrics = (
        ("executed_candidates", True),
        ("unique_trace_digests", True),
        ("unique_trace_ratio", True),
        ("rare_classes_reached", True),
        ("raw_model_states", True),
        ("semantic_states", True),
        ("semantic_transitions", True),
        ("unique_model_state_paths", True),
        ("failure_detected", True),
        ("failure_candidate_ordinal", False),
    )
    paired_rows: list[dict] = []
    blocks: dict[str, dict[str, dict]] = {}
    for (block, seed), modes in sorted(campaigns.items()):
        row = {"block": block, "seed": seed}
        baseline = modes[MODES[0]]
        facet = modes[MODES[1]]
        for metric, _ in metrics:
            baseline_value = metric_value(baseline, metric)
            facet_value = metric_value(facet, metric)
            row[f"baseline_{metric}"] = baseline_value
            row[f"facet_{metric}"] = facet_value
            row[f"difference_{metric}"] = facet_value - baseline_value
        row["baseline_queue_exhausted"] = baseline["metrics"]["queue_exhausted"]
        row["facet_queue_exhausted"] = facet["metrics"]["queue_exhausted"]
        paired_rows.append(row)
    for block in sorted({row["block"] for row in paired_rows}):
        block_rows = [row for row in paired_rows if row["block"] == block]
        blocks[block] = {}
        for metric, higher_is_better in metrics:
            differences = [row[f"difference_{metric}"] for row in block_rows]
            blocks[block][metric] = paired_bootstrap(
                differences,
                seed_material=f"{block}:{metric}",
                higher_is_better=higher_is_better,
            )
        blocks[block]["queue_exhaustion"] = {
            "baseline_count": sum(row["baseline_queue_exhausted"] for row in block_rows),
            "facet_count": sum(row["facet_queue_exhausted"] for row in block_rows),
        }
    output = {
        "schema": "modelfuzz-ng-stage7-paired-analysis-v1",
        "preregistration_sha256": PREREGISTRATION_SHA,
        "heldout_seed_list_sha256": SEED_LIST_SHA,
        "bootstrap_seed": BOOTSTRAP_SEED,
        "bootstrap_resamples": RESAMPLES,
        "difference_direction": "facet-only minus current-baseline",
        "blocks": blocks,
    }
    atomic_json(directory / "aggregate-statistics.json", output)
    fields = list(paired_rows[0])
    atomic_csv(directory / "paired-campaign-metrics.csv", paired_rows, fields)
    return output


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {Path(sys.argv[0]).name} RESULTS_DIR", file=sys.stderr)
        return 2
    output = analyze(Path(sys.argv[1]))
    print(json.dumps(output, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
