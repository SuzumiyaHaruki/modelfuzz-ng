#!/usr/bin/env python3
"""Safely compact ordinary Stage-1 probe raw artifacts.

The workflow is inventory -> classify -> extract replay schedules -> archive ->
zstd/tar verification -> deletion manifest -> exact deletion -> validation.
Only rank-local ``runs`` and ``replay-verification`` directories are eligible.
"""

from __future__ import annotations

import csv
import hashlib
import json
import os
import shutil
import subprocess
import sys
from functools import cmp_to_key
from pathlib import Path


SCHEMA = "raft-stage1-probe-retention-v1"


def read_results(path: Path) -> list[dict]:
    return [
        json.loads(line)
        for line in path.read_text(encoding="utf-8").splitlines()
        if line
    ]


def compare(left: dict, right: dict) -> int:
    fields = (
        ("goal_reached", True),
        ("best_completed_waypoints", True),
        ("best_distance", False),
        ("new_waypoint_reached", True),
        ("mutation_legal", True),
        ("mutation_executed", True),
        ("actions", False),
    )
    for field, higher in fields:
        if left[field] == right[field]:
            continue
        if higher:
            return -1 if left[field] > right[field] else 1
        return -1 if left[field] < right[field] else 1
    return -1 if left["handoff_stable_key"] < right["handoff_stable_key"] else 1


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def atomic_json(path: Path, value) -> None:
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    os.replace(temporary, path)


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: stage1_probe_retention.py REPOSITORY")
    repository = Path(sys.argv[1]).resolve()
    probe = (repository / ".tmp/breadth-depth-stage1/handoff-probe").resolve()
    if not probe.is_dir() or repository not in probe.parents:
        raise SystemExit("probe root is not a validated repository output")
    results = read_results(probe / "probe-results.jsonl")
    groups: dict[tuple[str, int], list[dict]] = {}
    for result in results:
        groups.setdefault((result["goal"], result["campaign_seed"]), []).append(result)
    posterior_best = {
        (goal, seed): sorted(values, key=cmp_to_key(compare))[0]["handoff_rank"]
        for (goal, seed), values in groups.items()
    }
    no_progress_sample: dict[str, tuple[int, int]] = {}
    for result in results:
        if (
            result["goal"] not in no_progress_sample
            and not result["goal_reached"]
            and result["mutation_executed"] > 0
            and not result["new_waypoint_reached"]
            and result["distance_delta"] <= 0
        ):
            no_progress_sample[result["goal"]] = (
                result["campaign_seed"],
                result["handoff_rank"],
            )

    targets: list[tuple[Path, dict]] = []
    retained: list[dict] = []
    for result in results:
        key = (result["goal"], result["campaign_seed"])
        full_reasons = []
        if result["campaign_seed"] == 9501:
            full_reasons.append("seed-9501-full")
        if result["handoff_rank"] == 1:
            full_reasons.append("rank-1")
        if result["handoff_rank"] == posterior_best[key]:
            full_reasons.append("campaign-posterior-best")
        if result["goal_reached"]:
            full_reasons.append("goal-reach")
        if result.get("error") or result["outcome"] in {
            "handoff_replay_failure",
            "candidate_execution_failure",
            "tlc_failure",
        }:
            full_reasons.append("failure-or-replay-mismatch")
        if no_progress_sample.get(result["goal"]) == (
            result["campaign_seed"],
            result["handoff_rank"],
        ):
            full_reasons.append("goal-no-progress-sample")
        rank = probe / result["goal"] / f"seed-{result['campaign_seed']}" / (
            f"rank-{result['handoff_rank']:02d}"
        )
        if not rank.is_dir():
            raise SystemExit(f"missing rank directory: {rank}")
        if full_reasons:
            retained.append(
                {
                    "rank_directory": str(rank.relative_to(repository)),
                    "mode": "full",
                    "reasons": full_reasons,
                }
            )
            atomic_json(
                rank / "artifact-retention.json",
                {
                    "schema_version": SCHEMA,
                    "mode": "full",
                    "reasons": full_reasons,
                    "global_corpus": "reference only",
                },
            )
            continue

        schedules = rank / "replay-schedules"
        schedules.mkdir(exist_ok=True)
        runs = rank / "runs"
        if runs.is_dir():
            for candidate in sorted(runs.glob("candidate-*")):
                if not candidate.is_dir():
                    continue
                for name in ("plan.json", "config.json"):
                    source = candidate / name
                    if source.is_file():
                        shutil.copy2(
                            source, schedules / f"{candidate.name}-{name}"
                        )
        rank_targets = []
        for name in ("runs", "replay-verification"):
            target = (rank / name).resolve()
            if not target.exists():
                continue
            if (
                probe not in target.parents
                or target.parent != rank.resolve()
                or target.name != name
                or not target.is_dir()
            ):
                raise SystemExit(f"unsafe retention target: {target}")
            metadata = {
                "goal": result["goal"],
                "seed": result["campaign_seed"],
                "rank": result["handoff_rank"],
                "stable_key": result["handoff_stable_key"],
                "relative_path": str(target.relative_to(repository)),
                "bytes": sum(
                    path.stat().st_size
                    for path in target.rglob("*")
                    if path.is_file()
                ),
            }
            targets.append((target, metadata))
            rank_targets.append(metadata["relative_path"])
        retained.append(
            {
                "rank_directory": str(rank.relative_to(repository)),
                "mode": "compact",
                "reasons": ["ordinary-probe"],
                "archived_paths": rank_targets,
            }
        )

    archive = (
        repository / ".tmp/breadth-depth-stage1/handoff-probe-ordinary-raw.tar.zst"
    )
    file_list = (
        repository / ".tmp/breadth-depth-stage1/handoff-probe-delete-plan.txt"
    )
    file_list.write_text(
        "".join(
            str(target.relative_to(repository)) + "\n"
            for target, _ in targets
        ),
        encoding="utf-8",
    )
    plan_tsv = probe / "retention-delete-plan.tsv"
    with plan_tsv.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(
            handle,
            fieldnames=[
                "goal",
                "seed",
                "rank",
                "stable_key",
                "relative_path",
                "bytes",
            ],
            delimiter="\t",
        )
        writer.writeheader()
        writer.writerows(metadata for _, metadata in targets)
    subprocess.run(
        [
            "tar",
            "--zstd",
            "-cf",
            str(archive),
            "-C",
            str(repository),
            "--files-from",
            str(file_list),
        ],
        check=True,
    )
    subprocess.run(["zstd", "-t", str(archive)], check=True)
    subprocess.run(["tar", "--zstd", "-tf", str(archive)], check=True, stdout=subprocess.DEVNULL)
    archive_digest = sha256(archive)

    deleted_tsv = probe / "retention-deleted.tsv"
    with deleted_tsv.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(
            handle,
            fieldnames=[
                "goal",
                "seed",
                "rank",
                "stable_key",
                "relative_path",
                "bytes",
                "archive_sha256",
            ],
            delimiter="\t",
        )
        writer.writeheader()
        for target, metadata in targets:
            # Resolve and validate again immediately before each exact removal.
            checked = target.resolve()
            if (
                checked != target
                or probe not in checked.parents
                or checked.name not in {"runs", "replay-verification"}
            ):
                raise SystemExit(f"target changed after archive: {target}")
            shutil.rmtree(checked)
            row = dict(metadata)
            row["archive_sha256"] = archive_digest
            writer.writerow(row)
    if any(target.exists() for target, _ in targets):
        raise SystemExit("one or more exact retention targets still exist")
    manifest = {
        "schema_version": SCHEMA,
        "archive": str(archive.relative_to(repository)),
        "archive_sha256": archive_digest,
        "archive_validation": "zstd-test+tar-list+sha256-passed",
        "deleted_directories": len(targets),
        "deleted_expanded_bytes": sum(item["bytes"] for _, item in targets),
        "full_rank_directories": sum(item["mode"] == "full" for item in retained),
        "compact_rank_directories": sum(
            item["mode"] == "compact" for item in retained
        ),
        "retention": retained,
        "ordinary_kept": [
            "settings and manifests",
            "probe result and StableKey",
            "final structured report and metrics",
            "goal progress journal",
            "replay Plan/config schedules",
            "compact pending-control summary and observation digest",
        ],
        "global_corpus": "referenced in place; not copied or modified",
    }
    atomic_json(probe / "artifact-retention.json", manifest)
    print(json.dumps(manifest, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
