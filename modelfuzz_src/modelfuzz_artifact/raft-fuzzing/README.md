# raft-fuzzing
Model coverage guided fuzzing of etcd raft

The scoped research questions, experiment variants, metrics, and decision rules are documented in
[`EXPERIMENT_PLAN.md`](EXPERIMENT_PLAN.md).

# Bug occurrences

- tlc: 41530, 
- trace: 15242
- random: 82371

## New-state attribution

The artifact contains the modified controlled TLC server under
`../tlc-controlled-with-benchmarks/tlc-controlled`. Its `/execute` response keeps the legacy `states` and `keys` fields
and additionally returns one transition record per input event.  Records distinguish `executed`,
`disabled`, and `ignored` events and preserve the original event index. `stateEventIndices` is aligned
with the abstract `states/keys` arrays and identifies which input event produced each retained coverage
state. The server snapshots every state before later events can mutate it and uses a fixed fingerprint
polynomial, so transition history is suffix-independent and keys are stable across JVM restarts.

From this directory, build and start it on port 2023 with:

```bash
cd ../tlc-controlled-with-benchmarks/tlc-controlled
ant -f customBuild.xml compile
ant -f customBuild.xml compile-test
ant -f customBuild.xml dist

java -Xms256m -Xmx2g \
  -jar dist/tla2tools_server.jar \
  ../tla-benchmarks/Raft/model/RAFT_5_3.tla \
  -config ../tla-benchmarks/Raft/model/RAFT_5_3.cfg \
  -mapperparams 'name=raft;abstract;port=2023'
```

Use `port=2023` in `-mapperparams`; this TLC fork's generic argument parser rejects the documented
`-serverport` option before the server-specific parser can consume it.

The `tlcstate` guider uses server transitions directly when they are available. The `phase-a`
command below accepts the modified server address explicitly.

An unmodified legacy server remains supported: if the response does not contain `transitions`,
the client falls back to event-prefix probing.  Attribution is opt-in for general comparisons;
`phase-a` always enables it. Recorded trace JSON files add:

- `event_origins`: the step, phase, scheduling-choice index, and delivery ordinal for each event;
- `new_state_attributions`: the new state, first event index, origin, and localization status.
- `tlc_transitions`: input index/name, mapped TLA+ action, status, and pre/post key;
- `tlc_state_event_indices`: event origins aligned with the abstract state trace;
- `tlc_provenance_available`: whether direct server provenance was used.

The current scheduling and mutation behavior is unchanged. `LastGuidance()` exposes the same
structured result in memory for the later localized-mutator stage.

### TLA+ model

The main experiment uses ModelFuzz's existing `RAFT_5_3.tla`, `RAFT_5_3.cfg`, and `raft_alt.tla`
under `../tlc-controlled-with-benchmarks/tla-benchmarks/Raft/model`. The enhanced model remains
only for model-sensitivity diagnostics.

## Phase A experiment

`phase-a` runs only the TLC-state guider with the original global ModelFuzz mutator. It always
enables attribution and writes a `summary.json`; `--record-traces` additionally records per-trace
attribution JSON files.

```bash
go run -buildvcs=false . phase-a \
  --tlc 127.0.0.1:2023 \
  --episodes 100 \
  --horizon 100 \
  --replicas 3 \
  --requests 1 \
  --seed 101 \
  --record-traces \
  --save results/phase-a/m1/seed-101
```

The seed is shared by random trace generation, all mutation operators, and each Raft node's
election-timeout random source.

`fuzzer_stats` separates seed generation, seed replay, mutation, and random executions;
`total_executions` includes seed generation, while `feedback_executions` equals `--episodes`.

For the Phase B localized variant, use the same command with `--localized-mutation`. The default
remains the original global ModelFuzz mutator.

Mixed exploration uses `--local-mutation-percent 50` or `70`; the summary records the actual
local/global mutation attempts.

Prefix-preserving suffix exploration uses `--prefix-preserving-mutation`. Each located new state
receives its own `MutPerTrace` children; each child leaves every scheduling choice through that
state's Origin step unchanged and mutates only the complete suffix. Operator counts are capped by
their original values and shrink when a late target has fewer suffix candidates. An unavailable
operator is skipped without crossing the preserved boundary. Initial states retain the original
global fallback behavior.
The run summary records prefix mutation attempts, guided attempts, initial-only global fallbacks,
successfully generated children, and rejected attempts whose suffix could not support the original
combined operator. It also reports target preservation and subsequent novelty, grouped by target
step bucket and mapped model action, so prefix correctness can be separated from suffix utility.

Fixed-budget logical-time exploration uses `--tick-density-mutation`. It records one `TickAll`
choice per step and moves one all-node logical tick between two boundaries after each located state.
The per-trace Tick total stays at `horizon * TicksPerStep`; `--max-tick-burst` bounds concentration.
Ready processing remains once per step and is not part of the mutation.
