package raft_test

import (
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
)

func pilotScenarios() []pilotScenario {
	election := plan.PlanSequence{
		Actions: []plan.PlanAction{
			timeout(1),
			message(plan.ActionDeliver, 1, 2, 1),
			message(plan.ActionDeliver, 2, 1, 1),
		},
		Metadata: map[string]string{"source": "examples/plans/election.json"},
	}
	contention := plan.PlanSequence{
		Actions: []plan.PlanAction{
			timeout(1),
			timeout(2),
			message(plan.ActionDeliver, 1, 3, 1),
			message(plan.ActionDeliver, 3, 1, 1),
		},
		Metadata: map[string]string{"source": "stage4_contention_from_adapter_election_helpers"},
	}
	replication := plan.PlanSequence{
		Actions: []plan.PlanAction{
			timeout(1),
			message(plan.ActionDeliver, 1, 2, 1),
			message(plan.ActionDeliver, 2, 1, 1),
			request(1, "1"),
			message(plan.ActionDeliver, 1, 2, 1),
			message(plan.ActionDeliver, 2, 1, 1),
			message(plan.ActionDeliver, 1, 2, 1),
			message(plan.ActionDeliver, 2, 1, 1),
			message(plan.ActionDeliver, 1, 2, 1),
		},
		Metadata: map[string]string{"source": "examples/plans/client-request-commit.json"},
	}
	crashRestart := plan.PlanSequence{
		Actions: []plan.PlanAction{
			crash(3),
			timeout(1),
			message(plan.ActionDeliver, 1, 2, 1),
			message(plan.ActionDeliver, 2, 1, 1),
			restart(3),
			message(plan.ActionDeliver, 1, 3, 1),
			message(plan.ActionDeliver, 3, 1, 1),
			message(plan.ActionDeliver, 1, 3, 1),
			message(plan.ActionDeliver, 3, 1, 1),
			message(plan.ActionDeliver, 1, 3, 1),
		},
		Metadata: map[string]string{"source": "examples/plans/follower-crash-restart.json"},
	}

	snapshotConfig := policy.SnapshotPartitionConfig{
		NodeIDs:           []core.NodeID{1, 2, 3},
		MaxValue:          pilotMaxValue,
		MaxLogIndex:       pilotMaxLogIndex,
		SnapshotThreshold: 3,
		RetainEntries:     1,
	}
	snapshotFailureConfig := snapshotConfig
	snapshotFailureConfig.FailFirstSnapshot = true
	snapshotDuplicateConfig := snapshotConfig
	snapshotDuplicateConfig.DuplicateSnapshot = true
	fastForwardConfig := policy.SnapshotFastForwardConfig{
		NodeIDs:           []core.NodeID{1, 2, 3},
		MaxValue:          pilotMaxValue,
		MaxLogIndex:       pilotMaxLogIndex,
		SnapshotThreshold: 4,
		RetainEntries:     1,
	}

	return []pilotScenario{
		staticPilotScenario(
			"election_stabilization", "A election stabilization",
			"examples/plans/election.json", 4401, election,
		),
		staticPilotScenario(
			"election_contention", "B election contention / split term",
			"internal/adapters/etcdraft/adapter_test.go election helpers", 4402, contention,
		),
		staticPilotScenario(
			"replication_lag_catchup", "C replication lag and catch-up",
			"examples/plans/client-request-commit.json", 4403, replication,
		),
		staticPilotScenario(
			"crash_restart_recovery", "D crash/restart recovery",
			"examples/plans/follower-crash-restart.json", 4404, crashRestart,
		),
		directedPilotScenario(
			"snapshot_catchup_success", "E snapshot catch-up success",
			"internal/policy.SnapshotPartition", 4405, 180, 3, 1,
			snapshotPartitionSource(snapshotConfig),
		),
		directedPilotScenario(
			"snapshot_failure_retry", "F snapshot transfer failure and retry",
			"internal/policy.SnapshotPartition(FailFirstSnapshot)", 4406, 180, 3, 1,
			snapshotPartitionSource(snapshotFailureConfig),
		),
		directedPilotScenario(
			"snapshot_duplicate_stale", "G snapshot alternative handling: duplicate/stale",
			"internal/policy.SnapshotPartition(DuplicateSnapshot)", 4407, 180, 3, 1,
			snapshotPartitionSource(snapshotDuplicateConfig),
		),
		directedPilotScenario(
			"snapshot_fast_forward", "G snapshot alternative handling: fast-forward",
			"internal/policy.SnapshotFastForward", 4408, 180, 4, 1,
			snapshotFastForwardSource(fastForwardConfig),
		),
	}
}

func staticPilotScenario(id, family, asset string, seed int64, sequence plan.PlanSequence) pilotScenario {
	return pilotScenario{
		ID: id, Family: family, SourceAsset: asset, Seed: seed,
		MaxPlanActions:    len(sequence.Actions) + 1,
		SnapshotThreshold: 3,
		RetainEntries:     1,
		InitializerPlan:   sequence.Copy(),
		NewSource:         staticScenarioSource(sequence),
	}
}

func directedPilotScenario(
	id, family, asset string,
	seed int64,
	maxActions int,
	threshold, retain uint64,
	factory sourceFactory,
) pilotScenario {
	return pilotScenario{
		ID: id, Family: family, SourceAsset: asset, Seed: seed,
		MaxPlanActions: maxActions, SnapshotThreshold: threshold, RetainEntries: retain,
		InitializerPlan: plan.PlanSequence{
			Actions:  []plan.PlanAction{timeout(core.NodeID(1 + seed%3))},
			Metadata: map[string]string{"source": asset, "role": "online_action_source_descriptor"},
		},
		NewSource: factory,
	}
}

func timeout(node core.NodeID) plan.PlanAction {
	return plan.PlanAction{Kind: plan.ActionTimeout, Node: node}
}

func crash(node core.NodeID) plan.PlanAction {
	return plan.PlanAction{Kind: plan.ActionCrash, Node: node}
}

func restart(node core.NodeID) plan.PlanAction {
	return plan.PlanAction{Kind: plan.ActionRestart, Node: node}
}

func request(node core.NodeID, value string) plan.PlanAction {
	return plan.PlanAction{Kind: plan.ActionRequest, Node: node, Request: value}
}

func message(kind plan.ActionKind, from, to core.NodeID, count int) plan.PlanAction {
	return plan.PlanAction{
		Kind: kind,
		Messages: &plan.MessageRangeSelector{
			Link:  core.LinkID{From: from, To: to},
			Count: count,
		},
	}
}
