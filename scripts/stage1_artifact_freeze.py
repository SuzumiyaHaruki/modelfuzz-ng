#!/usr/bin/env python3
"""Freeze and safely prune breadth/depth experiment artifacts.

The deletion mode only accepts exact paths emitted by the prepare mode, resolves
every target through realpath, verifies its archive when required, and refuses
targets outside this repository's .tmp directory.
"""

from __future__ import annotations

import argparse
import csv
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
from typing import Iterable


REPO = Path(__file__).resolve().parents[1]
TMP = (REPO / ".tmp").resolve()
FREEZE = REPO / "artifact-freeze" / "breadth-depth-stage1-precleanup"
ARCHIVES = FREEZE / "archives"
SOURCE_ENV = FREEZE / "source-and-env"
FROZEN = FREEZE / "frozen-structured"

FORMAL_ARCHIVE = ARCHIVES / "formal-local-raw-pruned.tar.zst"
GENERALIZATION_ARCHIVE = (
    ARCHIVES / "breadth-depth-generalization-threshold5-full.tar.zst"
)

GOAL_A = "snapshot-catchup-after-partition"
GOAL_B = "restart-then-higher-term-message"

EXPANDED_FORMAL_LOCAL_KEEP = {
    f".tmp/breadth-depth-formal/M5-facet-global-local/{GOAL_A}/seed-9501/local",
    f".tmp/breadth-depth-formal/M5-facet-global-local/{GOAL_A}/seed-9502/local",
    f".tmp/breadth-depth-formal/M5-facet-global-local/{GOAL_B}/seed-9501/local",
    f".tmp/breadth-depth-formal/M5-facet-global-local/{GOAL_B}/seed-9502/local",
}

CAMPAIGN_STRUCTURED_NAMES = {
    "breadth-depth-settings.json",
    "handoff-settings.json",
    "handoff-candidates.jsonl",
    "handoff-selected.json",
    "handoff-replay.jsonl",
    "local-phase-summary.json",
    "combined-summary.json",
    "coverage-growth-final.csv",
}

GLOBAL_STRUCTURED_NAMES = {
    "global-phase-summary.json",
    "global-corpus-manifest.json",
    "global-corpus-entries.jsonl",
    "coverage-growth-global.csv",
    "config.json",
    "experiment-settings.json",
    "coverage-guidance-settings.json",
    "coverage-guidance-summary.json",
    "cross-coverage-summary.json",
}


def run(command: list[str], *, check: bool = True) -> str:
    completed = subprocess.run(
        command,
        cwd=REPO,
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    return completed.stdout


def write_text(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value, encoding="utf-8")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def safe_tmp_path(path: Path) -> Path:
    resolved = path.resolve()
    if resolved == TMP or TMP not in resolved.parents:
        raise ValueError(f"unsafe deletion target outside repository .tmp: {path}")
    return resolved


def relative(path: Path) -> str:
    return path.relative_to(REPO).as_posix()


def formal_local_targets() -> list[str]:
    root = REPO / ".tmp" / "breadth-depth-formal"
    result = []
    for path in sorted(root.glob("M*/**/seed-*/local")):
        rel = relative(path)
        if rel not in EXPANDED_FORMAL_LOCAL_KEEP:
            result.append(rel)
    return result


def generalization_raw_targets() -> list[str]:
    root = REPO / ".tmp" / "breadth-depth-generalization-threshold5"
    result = []
    global_root = root / "_global"
    if global_root.exists():
        result.append(relative(global_root))
    result.extend(relative(path) for path in sorted(root.glob("M*/**/seed-*/local")))
    return result


def deletion_rows() -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    for rel in formal_local_targets():
        rows.append(
            {
                "path": rel,
                "classification": "archive_then_delete_formal_local_raw",
                "reason": (
                    "non-retained formal local raw; campaign settings, Handoff, "
                    "summary and coverage remain expanded"
                ),
                "archive": relative(FORMAL_ARCHIVE),
            }
        )
    for rel in generalization_raw_targets():
        rows.append(
            {
                "path": rel,
                "classification": "archive_then_delete_generalization_raw",
                "reason": (
                    "threshold=5 raw is preserved in a full verified archive; "
                    "root summaries and manifest remain expanded"
                ),
                "archive": relative(GENERALIZATION_ARCHIVE),
            }
        )
    direct = [
        (
            ".tmp/go-build-cache",
            "rebuildable_cache",
            "project-local Go build cache; rebuilt by go test/build",
        ),
        (
            ".tmp/go-path",
            "rebuildable_cache",
            "empty project-local temporary GOPATH",
        ),
        (
            ".tmp/breadth-depth-smoke-v5",
            "rebuildable_smoke",
            "completed smoke output; manifest and accepted formal artifacts supersede it",
        ),
    ]
    for rel, classification, reason in direct:
        if (REPO / rel).exists():
            rows.append(
                {
                    "path": rel,
                    "classification": classification,
                    "reason": reason,
                    "archive": "",
                }
            )
    return rows


def recursive_sizes(root: Path) -> tuple[list[Path], dict[Path, int]]:
    paths: list[Path] = []
    sizes: dict[Path, int] = {}
    for current, directories, files in os.walk(root, topdown=False, followlinks=False):
        current_path = Path(current)
        total = 0
        for name in files:
            path = current_path / name
            paths.append(path)
            try:
                total += path.lstat().st_size
            except FileNotFoundError:
                pass
        for name in directories:
            path = current_path / name
            paths.append(path)
            total += sizes.get(path, 0)
        sizes[current_path] = total
    paths.append(root)
    paths.sort(key=lambda item: relative(item) if item != root else ".tmp")
    return paths, sizes


def target_for(rel: str, targets: list[dict[str, str]]) -> dict[str, str] | None:
    for row in targets:
        target = row["path"]
        if rel == target or rel.startswith(target + "/"):
            return row
    return None


def is_critical(path: Path, rel: str) -> bool:
    name = path.name
    if rel.startswith(".tmp/breadth-depth-formal/_global/"):
        return name in GLOBAL_STRUCTURED_NAMES
    if rel.startswith(".tmp/breadth-depth-formal/"):
        if path.parent == REPO / ".tmp" / "breadth-depth-formal":
            return path.is_file()
        return name in CAMPAIGN_STRUCTURED_NAMES
    if rel.startswith(".tmp/breadth-depth-generalization-threshold5/"):
        root = REPO / ".tmp" / "breadth-depth-generalization-threshold5"
        return path.parent == root and path.is_file()
    if rel.startswith(".tmp/breadth-depth-control-mutant-regression/"):
        return path.is_file() and (
            name.endswith(".json")
            or name.endswith(".jsonl")
            or name.endswith(".csv")
            or name in {"plan.json", "trace.json", "config.json", "failure.json"}
        )
    if rel.startswith(".tmp/breadth-depth-pilot-summaries/"):
        return path.is_file()
    if name.endswith(".tar.zst") or rel == ".tmp/modelfuzz-ng":
        return path.is_file()
    return False


def write_inventory(
    destination: Path, targets: list[dict[str, str]], *, before: bool
) -> None:
    paths, sizes = recursive_sizes(TMP)
    with destination.open("w", newline="", encoding="utf-8") as output:
        writer = csv.writer(output, delimiter="\t", lineterminator="\n")
        writer.writerow(
            [
                "path",
                "type",
                "size_bytes",
                "mtime_utc",
                "purpose_classification",
                "keep",
                "delete_reason",
                "archive_location",
                "sha256",
            ]
        )
        for path in paths:
            try:
                stat = path.lstat()
            except FileNotFoundError:
                continue
            rel = relative(path)
            target = target_for(rel, targets) if before else None
            if path.is_symlink():
                kind = "symlink"
                size = stat.st_size
            elif path.is_dir():
                kind = "directory"
                size = sizes.get(path, 0)
            else:
                kind = "file"
                size = stat.st_size
            if target is None:
                classification = classify_keep(rel)
                keep = "true"
                reason = ""
                archive = ""
            else:
                classification = target["classification"]
                keep = "false"
                reason = target["reason"]
                archive = target["archive"]
            checksum = ""
            if path.is_file() and is_critical(path, rel):
                checksum = sha256(path)
            mtime = dt.datetime.fromtimestamp(
                stat.st_mtime, tz=dt.timezone.utc
            ).isoformat()
            writer.writerow(
                [
                    rel,
                    kind,
                    size,
                    mtime,
                    classification,
                    keep,
                    reason,
                    archive,
                    checksum,
                ]
            )


def classify_keep(rel: str) -> str:
    if rel.startswith(".tmp/breadth-depth-formal/_global/"):
        return "formal_global_corpus_preserve_expanded"
    if rel.startswith(".tmp/breadth-depth-formal/"):
        return "formal_structured_or_retained_sample"
    if rel.startswith(".tmp/breadth-depth-control-mutant-regression/"):
        return "control_mutant_replay_ddmin"
    if rel.startswith(".tmp/breadth-depth-pilot-summaries/"):
        return "pilot_structured_summary"
    if rel.endswith(".tar.zst"):
        return "verified_archive"
    if rel.startswith(".tmp/breadth-depth-generalization-threshold5/"):
        return "generalization_root_or_retained_structure"
    return "retain_unclassified_pending_manual_confirmation"


def link_or_copy(source: Path, destination: Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists():
        return
    try:
        os.link(source, destination)
    except OSError:
        shutil.copy2(source, destination)


def freeze_file(source: Path) -> None:
    link_or_copy(source, FROZEN / relative(source))


def freeze_tree(source: Path) -> None:
    if not source.exists():
        return
    for current, _, files in os.walk(source, followlinks=False):
        for name in files:
            freeze_file(Path(current) / name)


def freeze_structured() -> None:
    report = REPO / "docs" / "facet-waypoint-breadth-depth-combination.md"
    link_or_copy(report, FROZEN / "docs" / report.name)
    freeze_tree(REPO / "examples")

    formal = REPO / ".tmp" / "breadth-depth-formal"
    for path in formal.iterdir():
        if path.is_file():
            freeze_file(path)
    for path in formal.glob("M*/**/seed-*/*"):
        if path.is_file() and path.name in CAMPAIGN_STRUCTURED_NAMES:
            freeze_file(path)
    for path in formal.glob("_global/M*/seed-*/*"):
        if path.is_file() and path.name in GLOBAL_STRUCTURED_NAMES:
            freeze_file(path)

    freeze_tree(
        formal / "M5-facet-global-local" / GOAL_A / "seed-9501"
    )
    freeze_tree(REPO / ".tmp" / "breadth-depth-control-mutant-regression")
    freeze_tree(REPO / ".tmp" / "breadth-depth-pilot-summaries")

    generalization = REPO / ".tmp" / "breadth-depth-generalization-threshold5"
    for path in generalization.iterdir():
        if path.is_file():
            freeze_file(path)
    for path in (REPO / ".tmp").glob("breadth-depth-pilot-*.tar.zst"):
        freeze_file(path)
    e2e = REPO / ".tmp" / "breadth-depth-e2e.tar.zst"
    if e2e.exists():
        freeze_file(e2e)


def capture_source_and_env() -> None:
    SOURCE_ENV.mkdir(parents=True, exist_ok=True)
    commands = {
        "git-head.txt": ["git", "rev-parse", "HEAD"],
        "git-status-porcelain-v1.txt": ["git", "status", "--porcelain=v1"],
        "git-diff-binary.patch": ["git", "diff", "--binary"],
        "git-diff-cached-binary.patch": ["git", "diff", "--cached", "--binary"],
        "go-version.txt": ["go", "version"],
        "go-env.json": ["go", "env", "-json"],
        "java-version.txt": ["java", "-version"],
        "uname.txt": ["uname", "-a"],
        "disk-before.txt": ["df", "-B1", str(REPO)],
        "du-before.txt": ["du", "-x", "-B1", "--max-depth=2", ".tmp"],
        "git-diff-stat-before.txt": ["git", "diff", "--stat"],
    }
    for name, command in commands.items():
        write_text(SOURCE_ENV / name, run(command, check=False))
    write_text(
        SOURCE_ENV / "capture.json",
        json.dumps(
            {
                "captured_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
                "hostname": socket.gethostname(),
                "repository": str(REPO),
                "tmp_realpath": str(TMP),
                "build_command": (
                    "GOCACHE=/tmp/modelfuzz-stage1-gocache "
                    "go build -o .tmp/modelfuzz-ng ./cmd/modelfuzz-ng"
                ),
                "tlc_version": "1.8.0",
                "formal_environment": (
                    ".tmp/breadth-depth-formal/environment.json"
                ),
            },
            indent=2,
        )
        + "\n",
    )
    binaries = [
        REPO / ".tmp" / "modelfuzz-ng",
        REPO / ".tmp" / "modelfuzz-ng-breadth-depth",
        REPO / ".tmp" / "modelfuzz-ng-facet-guidance-final",
    ]
    checksum_lines = []
    for binary in binaries:
        if binary.exists():
            checksum_lines.append(f"{sha256(binary)}  {relative(binary)}")
    write_text(SOURCE_ENV / "experiment-binaries.sha256", "\n".join(checksum_lines) + "\n")

    raft = (REPO / ".." / "raft").resolve()
    raft_info = {
        "replace_path": str(raft),
        "go_mod_entry": "go.etcd.io/raft/v3 v3.7.0; replace => ../raft",
    }
    if raft.exists():
        raft_info["git_head"] = run(
            ["git", "-C", str(raft), "rev-parse", "HEAD"], check=False
        ).strip()
        raft_info["git_status"] = run(
            ["git", "-C", str(raft), "status", "--porcelain=v1"], check=False
        )
        write_text(
            SOURCE_ENV / "etcd-raft-git-diff-binary.patch",
            run(["git", "-C", str(raft), "diff", "--binary"], check=False),
        )
    write_text(
        SOURCE_ENV / "etcd-raft-local-replace.json",
        json.dumps(raft_info, indent=2) + "\n",
    )
    source_archive = SOURCE_ENV / "repository-source-tree.tar.zst"
    source_roots = [
        name
        for name in [
            "README.md",
            "go.mod",
            "go.sum",
            "cmd",
            "internal",
            "docs",
            "examples",
            "models",
            "scripts",
        ]
        if (REPO / name).exists()
    ]
    run(
        [
            "tar",
            "--zstd",
            "-cf",
            str(source_archive),
            "--",
            *source_roots,
        ]
    )
    write_text(
        SOURCE_ENV / "repository-source-tree.sha256",
        f"{sha256(source_archive)}  {relative(source_archive)}\n",
    )


def write_delete_plan(rows: list[dict[str, str]]) -> None:
    path = FREEZE / "delete-plan.tsv"
    with path.open("w", newline="", encoding="utf-8") as output:
        writer = csv.writer(output, delimiter="\t", lineterminator="\n")
        writer.writerow(
            ["path", "realpath", "size_bytes", "classification", "reason", "archive"]
        )
        for row in rows:
            target = REPO / row["path"]
            resolved = safe_tmp_path(target)
            size = directory_size(target)
            writer.writerow(
                [
                    row["path"],
                    str(resolved),
                    size,
                    row["classification"],
                    row["reason"],
                    row["archive"],
                ]
            )


def directory_size(path: Path) -> int:
    if not path.exists():
        return 0
    if path.is_file():
        return path.stat().st_size
    total = 0
    for current, _, files in os.walk(path, followlinks=False):
        for name in files:
            try:
                total += (Path(current) / name).lstat().st_size
            except FileNotFoundError:
                pass
    return total


def write_archive_lists(rows: list[dict[str, str]]) -> None:
    formal = [
        row["path"]
        for row in rows
        if row["archive"] == relative(FORMAL_ARCHIVE)
    ]
    write_text(FREEZE / "formal-local-raw-archive-list.txt", "\n".join(formal) + "\n")


def write_keep_manifest(rows: list[dict[str, str]]) -> None:
    manifest = {
        "schema_version": "breadth-depth-stage1-artifact-freeze-v1",
        "repository": str(REPO),
        "created_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "preserve_in_place": [
            ".tmp/breadth-depth-formal/_global",
            ".tmp/breadth-depth-formal root summaries",
            ".tmp/breadth-depth-formal campaign structured artifacts",
            f".tmp/breadth-depth-formal/M5-facet-global-local/{GOAL_A}/seed-9501",
            ".tmp/breadth-depth-control-mutant-regression",
            ".tmp/breadth-depth-pilot-summaries",
            ".tmp/breadth-depth-pilot-*.tar.zst",
        ],
        "expanded_formal_local_samples": sorted(EXPANDED_FORMAL_LOCAL_KEEP),
        "verified_archives_required_before_delete": [
            relative(FORMAL_ARCHIVE),
            relative(GENERALIZATION_ARCHIVE),
        ],
        "frozen_structured_root": relative(FROZEN),
        "global_corpus_policy": (
            "preserve expanded in original location; hard-link only key structured "
            "files, never archive or duplicate run trees"
        ),
        "delete_targets": rows,
    }
    write_text(
        FREEZE / "keep-manifest.json", json.dumps(manifest, indent=2) + "\n"
    )


def prepare() -> None:
    FREEZE.mkdir(parents=True, exist_ok=True)
    ARCHIVES.mkdir(parents=True, exist_ok=True)
    rows = deletion_rows()
    capture_source_and_env()
    freeze_structured()
    write_delete_plan(rows)
    write_archive_lists(rows)
    write_keep_manifest(rows)
    write_inventory(FREEZE / "inventory-before.tsv", rows, before=True)


def verify_archive(path: Path) -> None:
    if not path.exists() or path.stat().st_size == 0:
        raise ValueError(f"required archive missing or empty: {path}")
    run(["zstd", "-t", str(path)])
    listing = run(["tar", "--zstd", "-tf", str(path)])
    if not listing.strip():
        raise ValueError(f"archive contains no entries: {path}")


def record_archives() -> None:
    archives = sorted(ARCHIVES.glob("*.tar.zst"))
    if not archives:
        raise ValueError("no archives found")
    lines = []
    checks = []
    for archive in archives:
        verify_archive(archive)
        digest = sha256(archive)
        lines.append(f"{digest}  {relative(archive)}")
        checks.append(
            {
                "path": relative(archive),
                "bytes": archive.stat().st_size,
                "sha256": digest,
                "zstd_test": "passed",
                "tar_list": "passed",
            }
        )
    write_text(FREEZE / "archive-checksums.sha256", "\n".join(lines) + "\n")
    write_text(
        FREEZE / "archive-validation.json",
        json.dumps(
            {
                "schema_version": "breadth-depth-stage1-archive-validation-v1",
                "validated_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
                "archives": checks,
            },
            indent=2,
        )
        + "\n",
    )


def read_delete_plan() -> list[dict[str, str]]:
    with (FREEZE / "delete-plan.tsv").open(newline="", encoding="utf-8") as source:
        return list(csv.DictReader(source, delimiter="\t"))


def delete() -> None:
    plan = read_delete_plan()
    archive_cache: set[Path] = set()
    deleted = []
    for row in plan:
        target = REPO / row["path"]
        if not target.exists():
            raise ValueError(f"planned target disappeared before deletion: {target}")
        resolved = safe_tmp_path(target)
        if str(resolved) != row["realpath"]:
            raise ValueError(
                f"realpath changed after planning: {target}: {resolved} != {row['realpath']}"
            )
        archive_text = row["archive"]
        if archive_text:
            archive = REPO / archive_text
            if archive not in archive_cache:
                verify_archive(archive)
                archive_cache.add(archive)
        bytes_before = directory_size(target)
        if target.is_dir() and not target.is_symlink():
            shutil.rmtree(target)
        else:
            target.unlink()
        deleted.append(
            {
                "path": row["path"],
                "realpath": str(resolved),
                "bytes_before": str(bytes_before),
                "classification": row["classification"],
                "reason": row["reason"],
                "archive": archive_text,
                "deleted_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
            }
        )
    with (FREEZE / "deleted.tsv").open("w", newline="", encoding="utf-8") as output:
        fields = [
            "path",
            "realpath",
            "bytes_before",
            "classification",
            "reason",
            "archive",
            "deleted_at_utc",
        ]
        writer = csv.DictWriter(
            output, fieldnames=fields, delimiter="\t", lineterminator="\n"
        )
        writer.writeheader()
        writer.writerows(deleted)


def inventory_after() -> None:
    write_inventory(FREEZE / "inventory-after.tsv", [], before=False)
    write_text(SOURCE_ENV / "disk-after.txt", run(["df", "-B1", str(REPO)]))
    write_text(
        SOURCE_ENV / "du-after.txt",
        run(["du", "-x", "-B1", "--max-depth=2", ".tmp"]),
    )
    largest = run(
        [
            "bash",
            "-lc",
            "du -x -B1 --max-depth=5 .tmp artifact-freeze 2>/dev/null "
            "| sort -nr | head -30",
        ]
    )
    write_text(FREEZE / "largest-30-after.tsv", largest)


def write_retention_markers() -> None:
    plan = read_delete_plan()
    formal_rows = [
        row
        for row in plan
        if row["classification"] == "archive_then_delete_formal_local_raw"
    ]
    final_reports = [
        f"{row['path']}/final-report.json"
        for row in formal_rows
    ]
    with tempfile.TemporaryDirectory(prefix="stage1-retention-") as temporary:
        temporary_path = Path(temporary)
        report_list = temporary_path / "final-reports.txt"
        write_text(report_list, "\n".join(final_reports) + "\n")
        run(
            [
                "tar",
                "--zstd",
                "-xf",
                str(FORMAL_ARCHIVE),
                "-C",
                str(temporary_path),
                "-T",
                str(report_list),
            ]
        )
        archive_digest = sha256(FORMAL_ARCHIVE)
        for row, member in zip(formal_rows, final_reports):
            report_path = temporary_path / member
            report = json.loads(report_path.read_text(encoding="utf-8"))
            campaign = REPO / row["path"]
            campaign = campaign.parent
            combined = json.loads(
                (campaign / "combined-summary.json").read_text(encoding="utf-8")
            )
            local = combined["local_phase"]
            marker = {
                "schema_version": "raft-breadth-depth-artifact-retention-v1",
                "raw_pruned": True,
                "pruned_paths": ["local"],
                "retained": [
                    "breadth-depth-settings.json",
                    "handoff-settings.json",
                    "handoff-candidates.jsonl",
                    "handoff-selected.json",
                    "handoff-replay.jsonl",
                    "local-phase-summary.json",
                    "combined-summary.json",
                    "coverage-growth-final.csv",
                ],
                "compressed": [member],
                "discarded": [],
                "archive_path": relative(FORMAL_ARCHIVE),
                "archive_sha256": archive_digest,
                "archive_validation": "zstd+tar+sha256-passed",
                "combined_stable_key": combined["stable_key"],
                "local_stable_key": local["stable_key"],
                "local_candidates": local["candidates"],
                "tlc_executed_runs": report["tlc_executed_runs"],
                "runtime_statuses": report["runtime_statuses"],
                "final_report_sha256": sha256(report_path),
                "recorded_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
            }
            if marker["local_candidates"] != report["candidate_plans"]:
                raise ValueError(
                    f"candidate mismatch while recording retention: {campaign}"
                )
            write_text(
                campaign / "artifact-retention.json",
                json.dumps(marker, indent=2) + "\n",
            )

    formal_root = REPO / ".tmp" / "breadth-depth-formal"
    write_text(
        formal_root / "artifact-retention.json",
        json.dumps(
            {
                "schema_version": "raft-breadth-depth-artifact-retention-v1",
                "policy": "stage1-precleanup-formal",
                "global_corpus": "retained-expanded-read-only",
                "campaign_structured": "retained-expanded",
                "raw_local": (
                    "four expanded samples retained; remaining local directories "
                    "stored in verified archive"
                ),
                "archive_path": relative(FORMAL_ARCHIVE),
                "archive_sha256": sha256(FORMAL_ARCHIVE),
                "archive_validation": "zstd+tar+sha256-passed",
                "recorded_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
            },
            indent=2,
        )
        + "\n",
    )
    generalization_root = (
        REPO / ".tmp" / "breadth-depth-generalization-threshold5"
    )
    write_text(
        generalization_root / "artifact-retention.json",
        json.dumps(
            {
                "schema_version": "raft-breadth-depth-artifact-retention-v1",
                "policy": "stage1-precleanup-generalization",
                "root_structured": "retained-expanded",
                "raw_campaigns": "verified-full-archive",
                "archive_path": relative(GENERALIZATION_ARCHIVE),
                "archive_sha256": sha256(GENERALIZATION_ARCHIVE),
                "archive_validation": "zstd+tar+sha256-passed",
                "recorded_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
            },
            indent=2,
        )
        + "\n",
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "mode",
        choices=[
            "prepare",
            "record-archives",
            "delete",
            "retention-markers",
            "inventory-after",
        ],
    )
    arguments = parser.parse_args()
    if arguments.mode == "prepare":
        prepare()
    elif arguments.mode == "record-archives":
        record_archives()
    elif arguments.mode == "delete":
        delete()
    elif arguments.mode == "retention-markers":
        write_retention_markers()
    else:
        inventory_after()
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"stage1 artifact freeze failed: {error}", file=sys.stderr)
        raise
