# Breadth/depth Stage-1 Handoff diagnosis

## Scope and frozen semantics

This stage isolates the Handoff boundary. It introduces no LLM, multi-Agent,
Bandit, RL, unified floating score, CFT change, or change to Facets, Goals,
Waypoints, staged Distance, prefix preservation, focused Advisor, strict TLC,
Mapper, or Oracle semantics.

## Safe cleanup

- Exact deleted targets: 120
- Released bytes recorded by deletion manifest: 7391036724
- Filesystem before cleanup:

  `/dev/sda3      78546804736 64671260672 10396475392  87% /home/test/Desktop`

- Filesystem after cleanup:

  `/dev/sda3      78546804736 57295269888 17772466176  77% /home/test/Desktop`

- Formal local raw archive SHA-256: `339aca61d162c5b0a85bb32f8d239b749b2698979c4645442c27bf6aa31dbcc2`
- Threshold-5 archive SHA-256: `8d3b5b44dda1566bdf1966b97b333ba10cf2b17f2cb4226be21f5e423dcfba2c`
- Archive verification: zstd stream, tar listing, and SHA-256 all passed.
- Global Corpus stayed expanded and read-only; seed 9501 and retained mutant
  replay both completed; the formal root summary was rebuilt with
  `-skip-completed=true`.

The authoritative inventories, exact delete plan, deletion record, before/after
inventory, archive checksums, and largest-path lists are under
`artifact-freeze/breadth-depth-stage1-precleanup/`.

## Local-only-30

- `snapshot-catchup-after-partition`: Goal 8/10 (Wilson 95% [0.490, 0.943]); M5 local-30 3/10; paired Local-only/M5/both/neither = 6/1/2/1; legal mutation rate 1.000; M1-90 reference 10/10.
- `restart-then-higher-term-message`: Goal 10/10 (Wilson 95% [0.722, 1.000]); M5 local-30 8/10; paired Local-only/M5/both/neither = 2/0/8/0; legal mutation rate 1.000; M1-90 reference 10/10.

Unsuccessful campaigns are censored: budget exhaustion is never reported as a
first-hit time. Per-campaign initial/final Distance, Waypoint, candidate,
action, time, mutation legality, strict TLC, Online/Offline consistency, and
artifact paths are in `local-only-30-campaigns.jsonl/csv`.

## Top-K Handoff counterfactual probe

- Campaigns: 20
- Probe seed results: 160
- Rank-1 posterior-best: 11/20
- Unselected seed strictly posterior-better: 9/20
- Rank-1 Goal reach: 5/20
- Best-of-K Goal reach: 12/20
- Best-of-K Waypoint improvements: 8
- Best-of-K same-Waypoint Distance improvements: 0
- Mean per-campaign Spearman static/posterior rank: 0.4416666666666667

- `snapshot-catchup-after-partition`: rank-1 best 9/10; unselected better 1/10; rank-1/best-of-K Goal reach 3/4; Waypoint improvements 1; mean Spearman 0.933.
- `restart-then-higher-term-message`: rank-1 best 2/10; unselected better 8/10; rank-1/best-of-K Goal reach 2/8; Waypoint improvements 7; mean Spearman -0.050.

The posterior comparator is the frozen deterministic lexicographic order, not a
floating score. Rank legal rates, initial progress, semantic-class and Facet
continuation tables are in `probe-summary.json`.

Probe retention kept 53 rank
directories in full and compacted 107
ordinary ranks. Before exact deletion, 214
raw/replay directories were archived and validated. Archive SHA-256:
`123800bce47bca324faf57adebd04ddb4e2c2d7995315269902b3ffb7acf7f71`. A compacted ordinary Plan/config was
then executed again under strict TLC and completed with no Oracle finding.

## Seed 9501 root cause

# Seed 9501 Handoff diagnosis

## Reproduction

- Goal: snapshot-catchup-after-partition
- Selected StableKey: 29e8cb1b96448b319cebc4d40ef3953077154eb87a8911617f4b00e1cfc9f32e
- Prefix length: 180
- Max actions per Plan: 180
- Stable reproduction of 30 rejected / 0 executed: true
- Reproduced mutation attempts: 30
- Rejected at max-actions precheck: 30
- Valid candidates: 0
- Executed candidates/TLC runs: 0/0

## Root cause

This is a deterministic Handoff–local-search length-budget interface defect.
Prefix preservation requires the local mutation to append an action, while the
rank-1 Handoff prefix already contains 180 PlanActions, exactly the frozen
per-Plan maximum. MutateTowardWaypointWithOptions rejects at its pre-Advisor
length check, so the focused Advisor is never invoked and strict TLC has no
candidate to execute.

It is not evidence that the protocol state is a dead-end: the compact state
snapshot exposes controllable protocol actions. It is not a TLC, Oracle,
Mapper, action-legality, or statistics-only failure. The legality policy is
behaving as implemented; the incompatible Handoff prefix was admitted without
checking remaining local append capacity.

Successful same-Goal control: successful-control-seed-9502-rank-1: prefix=113 remaining=67 completed=5 distance=4 legal=3/3.

## Recommendation (not applied)

For the next bounded correction, add remaining append capacity as a Handoff
eligibility precondition (or reserve a documented local suffix budget) before
static ranking. Keep the frozen Goal, Waypoints, staged Distance, prefix
preservation, focused Advisor, TLC, Mapper, and Oracle semantics unchanged.


## Commands

```text
scripts/stage1_artifact_freeze.py prepare/delete/inventory-after/retention-markers
.tmp/modelfuzz-ng breadth-depth-benchmark -manifest examples/breadth-depth-stage1-local-only-30.json -output .tmp/breadth-depth-stage1/local-only-30 -skip-completed=true
.tmp/modelfuzz-ng handoff-probe-benchmark -manifest examples/breadth-depth-stage1-handoff-probe.json -output .tmp/breadth-depth-stage1/handoff-probe -skip-completed=true
.tmp/modelfuzz-ng handoff-diagnose -source .tmp/breadth-depth-formal -output .tmp/breadth-depth-stage1/seed-9501-diagnosis -config examples/config-facet-guidance-control.json -goal snapshot-catchup-after-partition -seed 9501 -control-seed 9502 -probe-root .tmp/breadth-depth-stage1/handoff-probe
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
