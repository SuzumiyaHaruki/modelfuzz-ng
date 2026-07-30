package main

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
)

func TestGoalRelevantInteractionRequiresTargetSpecificSemantics(t *testing.T) {
	observation := coverageguidance.CoverageObservation{
		InteractionKeys: map[string][]coverageguidance.CoverageValue{
			"snapshot_recovery": {{
				Value: `{"value":{"snapshot_mode":"no-snapshot","snapshot_outcome":"none"}}`,
			}},
			"recovery_term_relation": {{
				Value: `{"value":{"recovery_phase":"node-crashed","message_term_relation":"higher"}}`,
			}},
		},
	}
	if hasGoalRelevantInteraction(observation, goalsearch.GoalSnapshotCatchUpAfterPartition) ||
		hasGoalRelevantInteraction(observation, goalsearch.GoalRestartHigherTermMessage) {
		t.Fatal("background interaction values must not count as target-relevant")
	}
	observation.InteractionKeys["snapshot_recovery"] = []coverageguidance.CoverageValue{{
		Value: `{"value":{"snapshot_mode":"snapshot-available","snapshot_outcome":"installed"}}`,
	}}
	observation.InteractionKeys["recovery_term_relation"] = []coverageguidance.CoverageValue{{
		Value: `{"value":{"recovery_phase":"restarted-waiting-catch-up","message_term_relation":"higher"}}`,
	}}
	if !hasGoalRelevantInteraction(observation, goalsearch.GoalSnapshotCatchUpAfterPartition) ||
		!hasGoalRelevantInteraction(observation, goalsearch.GoalRestartHigherTermMessage) {
		t.Fatal("target-specific interaction values were not recognized")
	}
}
