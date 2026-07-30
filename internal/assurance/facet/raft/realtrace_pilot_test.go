package raft_test

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestRealTraceFacetPilot(t *testing.T) {
	scenarios := pilotScenarios()
	if len(scenarios)*3 > 40 {
		t.Fatalf("candidate budget=%d exceeds 40", len(scenarios)*3)
	}
	reports := make([]pilotRun, 0, len(scenarios)*3)
	for _, scenario := range scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			repetitions := make([]pilotRun, 3)
			for repeat := range repetitions {
				repetitions[repeat] = runPilotScenario(t, scenario)
			}
			first := canonicalSemanticSummary(repetitions[0])
			for repeat := 1; repeat < len(repetitions); repeat++ {
				if !bytes.Equal(first, canonicalSemanticSummary(repetitions[repeat])) {
					t.Fatalf(
						"same Plan/seed repetition %d changed\nfirst=%s\ncurrent=%s",
						repeat, first, canonicalSemanticSummary(repetitions[repeat]),
					)
				}
			}
			reports = append(reports, repetitions...)
		})
	}

	assertNonDegeneratePilot(t, reports)
	assertCrossFacetOrthogonality(t, reports)
	assertSemanticCompression(t, reports)
	assertSnapshotMarkersAreReal(t, reports)

	loggable := make([]pilotRun, 0, len(scenarios))
	for index := 0; index < len(reports); index += 3 {
		loggable = append(loggable, reports[index])
	}
	for index := range loggable {
		loggable[index].Trace = core.Trace{}
	}
	frequencies := make(map[string]int)
	totalPlanActions, totalConcreteActions, totalSteps, totalEffects, totalModelEvents := 0, 0, 0, 0, 0
	for _, report := range reports {
		totalPlanActions += report.PlanActions
		totalConcreteActions += report.ConcreteActions
		totalSteps += report.TraceSteps
		totalEffects += report.Effects
		totalModelEvents += report.ModelEvents
		for _, evaluation := range report.Facets {
			for _, key := range evaluation.Keys {
				frequencies[key.Canonical]++
			}
		}
	}
	encoded, err := json.MarshalIndent(struct {
		Schema               string         `json:"schema"`
		Candidates           int            `json:"candidates"`
		ScenarioRepetitions  int            `json:"scenario_repetitions"`
		TotalPlanActions     int            `json:"total_plan_actions"`
		TotalConcreteActions int            `json:"total_concrete_actions"`
		TotalTraceSteps      int            `json:"total_trace_steps"`
		TotalEffects         int            `json:"total_effects"`
		TotalModelEvents     int            `json:"total_model_events"`
		StatusCounts         map[string]int `json:"status_counts"`
		ClassFrequencies     map[string]int `json:"candidate_presence_frequency"`
		RepresentativeRuns   []pilotRun     `json:"representative_runs"`
	}{
		Schema:               "stage4-real-trace-facet-pilot-v1",
		Candidates:           len(reports),
		ScenarioRepetitions:  3,
		TotalPlanActions:     totalPlanActions,
		TotalConcreteActions: totalConcreteActions,
		TotalTraceSteps:      totalSteps,
		TotalEffects:         totalEffects,
		TotalModelEvents:     totalModelEvents,
		StatusCounts:         statusCounts(reports),
		ClassFrequencies:     frequencies,
		RepresentativeRuns:   loggable,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("STAGE4_PILOT_SUMMARY=%s", encoded)
}

func assertNonDegeneratePilot(t *testing.T, reports []pilotRun) {
	t.Helper()
	election := make(map[string]struct{})
	replication := make(map[string]struct{})
	snapshot := make(map[string]struct{})
	for _, report := range reports {
		for _, key := range facetByID(report, "raft.election_role_term_shape").Keys {
			election[key.ClassID] = struct{}{}
		}
		for _, key := range facetByID(report, "raft.replication_alignment_shape").Keys {
			replication[key.ClassID] = struct{}{}
		}
		for _, key := range facetByID(report, "raft.snapshot_lifecycle_event").Keys {
			snapshot[key.ClassID] = struct{}{}
		}
	}
	if len(election) < 3 {
		t.Fatalf("election classes=%v, want at least 3", sortedKeys(election))
	}
	hasNoneOrCandidate, hasOne := false, false
	for classID := range election {
		hasNoneOrCandidate = hasNoneOrCandidate ||
			contains(classID, "leaders_none") || contains(classID, "candidates_some")
		hasOne = hasOne || contains(classID, "leaders_one")
	}
	if !hasNoneOrCandidate || !hasOne {
		t.Fatalf("election threshold failed: %v", sortedKeys(election))
	}

	if len(replication) < 2 ||
		!containsClass(replication, "log_aligned_commit_aligned_applied_aligned") {
		t.Fatalf("replication threshold failed: %v", sortedKeys(replication))
	}
	hasDiverged := false
	for classID := range replication {
		hasDiverged = hasDiverged || contains(classID, "diverged")
	}
	if !hasDiverged {
		t.Fatalf("replication has no diverged class: %v", sortedKeys(replication))
	}

	requiredSnapshot := []string{"snapshot_created", "log_compacted", "snapshot_sent"}
	for _, classID := range requiredSnapshot {
		if !containsClass(snapshot, classID) {
			t.Fatalf("snapshot missing %s: %v", classID, sortedKeys(snapshot))
		}
	}
	hasDelivery := containsClass(snapshot, "snapshot_delivered") ||
		containsClass(snapshot, "snapshot_applied") ||
		containsClass(snapshot, "snapshot_fast_forwarded") ||
		containsClass(snapshot, "snapshot_rejected_or_stale")
	hasStatus := containsClass(snapshot, "snapshot_status_succeeded") ||
		containsClass(snapshot, "snapshot_status_failed") ||
		containsClass(snapshot, "snapshot_status_ignored")
	if len(snapshot) < 5 || !hasDelivery || !hasStatus {
		t.Fatalf("snapshot threshold failed: %v", sortedKeys(snapshot))
	}
}

func assertSemanticCompression(t *testing.T, reports []pilotRun) {
	t.Helper()
	type firstTrace struct {
		digest string
		id     string
	}
	seen := make(map[string]firstTrace)
	for _, report := range reports {
		for _, evaluation := range report.Facets {
			for _, key := range evaluation.Keys {
				if previous, exists := seen[key.Canonical]; exists &&
					previous.digest != report.TraceDigest {
					t.Logf(
						"semantic compression witness: %s/%s and %s/%s share %s",
						previous.id, previous.digest, report.ScenarioID, report.TraceDigest, key.Canonical,
					)
					return
				}
				seen[key.Canonical] = firstTrace{digest: report.TraceDigest, id: report.ScenarioID}
			}
		}
	}
	t.Fatal("no different TraceDigest candidates shared a FacetKey")
}

func assertCrossFacetOrthogonality(t *testing.T, reports []pilotRun) {
	t.Helper()
	type witnessed struct {
		scenario    string
		step        int
		election    string
		replication string
	}
	states := make([]witnessed, 0)
	for _, report := range reports {
		for stepIndex, step := range report.Trace.Steps {
			election, electionOK := testElectionShape(step.NodesAfter)
			replication, replicationOK := testReplicationShape(step.NodesAfter)
			if electionOK && replicationOK {
				states = append(states, witnessed{
					scenario: report.ScenarioID, step: stepIndex,
					election: election, replication: replication,
				})
			}
		}
	}
	for left := 0; left < len(states); left++ {
		for right := left + 1; right < len(states); right++ {
			if states[left].election == states[right].election &&
				states[left].replication != states[right].replication ||
				states[left].replication == states[right].replication &&
					states[left].election != states[right].election {
				t.Logf("cross-Facet orthogonality witness: %+v / %+v", states[left], states[right])
				return
			}
		}
	}
	t.Fatal("no pair of real trace states demonstrated cross-Facet orthogonality")
}

func assertSnapshotMarkersAreReal(t *testing.T, reports []pilotRun) {
	t.Helper()
	for _, report := range reports {
		snapshot := facetByID(report, "raft.snapshot_lifecycle_event")
		for _, key := range snapshot.Keys {
			if key.Occurrence.Kind != facet.OccurrenceTransitionEffect ||
				key.Occurrence.StepIndex == nil || key.Occurrence.EffectIndex == nil {
				t.Fatalf("%s snapshot occurrence is not a real transition effect: %+v", report.ScenarioID, key)
			}
			step := report.Trace.Steps[*key.Occurrence.StepIndex]
			effect := step.Effects[*key.Occurrence.EffectIndex]
			if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil ||
				!contains(effect.ModelEvent.Name, "raft.snapshot_") &&
					effect.ModelEvent.Name != "raft.log_compacted" {
				t.Fatalf("%s snapshot key is not backed by Adapter marker: %+v", report.ScenarioID, effect)
			}
		}
	}
}

func testElectionShape(nodes []core.NodeObservation) (string, bool) {
	if len(nodes) == 0 {
		return "", false
	}
	leaders, candidates := 0, 0
	terms := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		role, roleOK := node.Semantic["role"].(string)
		term, termOK := node.Semantic["term"].(uint64)
		if !roleOK || !termOK {
			return "", false
		}
		if role == "leader" {
			leaders++
		}
		if role == "candidate" {
			candidates++
		}
		terms = append(terms, term)
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i] < terms[j] })
	leaderPart := "leaders_multiple"
	if leaders == 0 {
		leaderPart = "leaders_none"
	} else if leaders == 1 {
		leaderPart = "leaders_one"
	}
	candidatePart := "candidates_none"
	if candidates > 0 {
		candidatePart = "candidates_some"
	}
	termPart := "terms_uniform"
	if terms[0] != terms[len(terms)-1] {
		termPart = "terms_split"
	}
	return leaderPart + "_" + candidatePart + "_" + termPart, true
}

func testReplicationShape(nodes []core.NodeObservation) (string, bool) {
	if len(nodes) == 0 {
		return "", false
	}
	last := make([]uint64, len(nodes))
	commit := make([]uint64, len(nodes))
	applied := make([]uint64, len(nodes))
	for index, node := range nodes {
		var ok bool
		last[index], ok = node.Semantic["last_index"].(uint64)
		if !ok {
			return "", false
		}
		commit[index], ok = node.Semantic["commit"].(uint64)
		if !ok {
			return "", false
		}
		applied[index], ok = node.Semantic["applied"].(uint64)
		if !ok {
			return "", false
		}
	}
	return alignedPart("log", last) + "_" +
		alignedPart("commit", commit) + "_" +
		alignedPart("applied", applied), true
}

func alignedPart(name string, values []uint64) string {
	for index := 1; index < len(values); index++ {
		if values[index] != values[0] {
			return name + "_diverged"
		}
	}
	return name + "_aligned"
}

func containsClass(set map[string]struct{}, classID string) bool {
	_, exists := set[classID]
	return exists
}

func contains(value, fragment string) bool {
	return bytes.Contains([]byte(value), []byte(fragment))
}
