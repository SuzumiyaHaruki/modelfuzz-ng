package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type coverageSets struct {
	raw, v2       map[int64]struct{}
	facets        map[string]map[int64]struct{}
	interactions  map[string]map[int64]struct{}
	semanticTrace map[string]struct{}
}

func newCoverageSets() *coverageSets {
	result := &coverageSets{
		raw: make(map[int64]struct{}), v2: make(map[int64]struct{}),
		facets:        make(map[string]map[int64]struct{}),
		interactions:  make(map[string]map[int64]struct{}),
		semanticTrace: make(map[string]struct{}),
	}
	for _, name := range []string{"election", "replication", "snapshot", "recovery", "network"} {
		result.facets[name] = make(map[int64]struct{})
	}
	for _, name := range []string{
		"election_network", "replication_network", "snapshot_recovery", "recovery_term_relation",
	} {
		result.interactions[name] = make(map[int64]struct{})
	}
	return result
}

func (s *coverageSets) add(observation coverageguidance.CoverageObservation) {
	addCoverageValues(s.raw, observation.RawTLCFingerprints)
	addCoverageValues(s.v2, observation.V2StateKeys)
	for name, values := range observation.FacetKeys {
		if s.facets[name] == nil {
			s.facets[name] = make(map[int64]struct{})
		}
		addCoverageValues(s.facets[name], values)
	}
	for name, values := range observation.InteractionKeys {
		if s.interactions[name] == nil {
			s.interactions[name] = make(map[int64]struct{})
		}
		addCoverageValues(s.interactions[name], values)
	}
	if observation.SemanticTraceDigest != "" {
		s.semanticTrace[observation.SemanticTraceDigest] = struct{}{}
	}
}

func (s *coverageSets) summary() breadthdepth.CoverageSummary {
	result := breadthdepth.CoverageSummary{
		Facets: make(map[string]int), Interactions: make(map[string]int),
	}
	if s == nil {
		return result
	}
	result.Raw, result.V2, result.SemanticTraces =
		len(s.raw), len(s.v2), len(s.semanticTrace)
	for name, values := range s.facets {
		result.Facets[name] = len(values)
	}
	for name, values := range s.interactions {
		result.Interactions[name] = len(values)
	}
	return result
}

func coverageDifference(final, before *coverageSets) breadthdepth.CoverageSummary {
	result := breadthdepth.CoverageSummary{
		Facets: make(map[string]int), Interactions: make(map[string]int),
	}
	if final == nil {
		return result
	}
	result.Raw = setDifferenceCount(final.raw, before.raw)
	result.V2 = setDifferenceCount(final.v2, before.v2)
	result.SemanticTraces = stringSetDifferenceCount(final.semanticTrace, before.semanticTrace)
	for name, values := range final.facets {
		result.Facets[name] = setDifferenceCount(values, before.facets[name])
	}
	for name, values := range final.interactions {
		result.Interactions[name] = setDifferenceCount(values, before.interactions[name])
	}
	return result
}

func setDifferenceCount(left, right map[int64]struct{}) int {
	count := 0
	for key := range left {
		if _, exists := right[key]; !exists {
			count++
		}
	}
	return count
}

func stringSetDifferenceCount(left, right map[string]struct{}) int {
	count := 0
	for key := range left {
		if _, exists := right[key]; !exists {
			count++
		}
	}
	return count
}

func collectCampaignCoverage(
	campaignDirectory, globalDirectory, localDirectory string,
	config cliConfig,
	localReport *goalSearchReport,
) (breadthdepth.CoverageSummary, breadthdepth.CoverageSummary, bool, error) {
	globalSets, finalSets := newCoverageSets(), newCoverageSets()
	rows := [][]string{{
		"phase", "phase_candidate", "total_candidate", "total_actions", "raw", "v2",
		"election", "replication", "snapshot", "recovery", "network",
		"election_network", "replication_network", "snapshot_recovery",
		"recovery_term_relation", "semantic_traces",
	}}
	totalCandidate, totalActions := 0, 0
	if globalDirectory != "" {
		report := readExperimentReport(globalDirectory)
		observations, err := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
			filepath.Join(globalDirectory, "coverage-observations.jsonl"), report.CompletedRuns)
		if err != nil {
			return breadthdepth.CoverageSummary{}, breadthdepth.CoverageSummary{}, false, err
		}
		for index, observation := range observations {
			globalSets.add(observation)
			finalSets.add(observation)
			totalCandidate++
			totalActions += observation.ActionCount
			rows = append(rows, coverageGrowthRow(
				"global", index+1, totalCandidate, totalActions, finalSets.summary()))
		}
	}
	if localReport != nil {
		for candidate := 0; candidate < localReport.Candidates; candidate++ {
			runDirectory := filepath.Join(
				localDirectory, "runs", fmt.Sprintf("candidate-%06d", candidate))
			var execution engine.Result
			if err := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &execution); err != nil {
				return breadthdepth.CoverageSummary{}, breadthdepth.CoverageSummary{}, false,
					fmt.Errorf("read local candidate %d result: %w", candidate, err)
			}
			var sequence plan.PlanSequence
			if err := persistence.ReadJSON(filepath.Join(runDirectory, "plan.json"), &sequence); err != nil {
				return breadthdepth.CoverageSummary{}, breadthdepth.CoverageSummary{}, false,
					fmt.Errorf("read local candidate %d plan: %w", candidate, err)
			}
			observation, err := coverageanalysis.BuildCoverageObservation(
				coverageanalysis.ObservationInput{
					RunID:       fmt.Sprintf("local-%06d", candidate),
					CandidateID: fmt.Sprintf("local-%06d", candidate),
					Source:      "local-goal", Plan: sequence, Result: execution,
					ModelConfig: config.Model,
				})
			if err != nil {
				return breadthdepth.CoverageSummary{}, breadthdepth.CoverageSummary{}, false, err
			}
			finalSets.add(observation)
			totalCandidate++
			totalActions += observation.ActionCount
			rows = append(rows, coverageGrowthRow(
				"local", candidate+1, totalCandidate, totalActions, finalSets.summary()))
		}
	}
	if err := writeCSVRows(filepath.Join(campaignDirectory, "coverage-growth-final.csv"), rows); err != nil {
		return breadthdepth.CoverageSummary{}, breadthdepth.CoverageSummary{}, false, err
	}
	finalSummary := finalSets.summary()
	retained := coverageContains(finalSets, globalSets)
	return finalSummary, coverageDifference(finalSets, globalSets), retained, nil
}

func coverageGrowthRow(
	phase string, phaseCandidate, totalCandidate, actions int,
	summary breadthdepth.CoverageSummary,
) []string {
	return []string{
		phase, strconv.Itoa(phaseCandidate), strconv.Itoa(totalCandidate), strconv.Itoa(actions),
		strconv.Itoa(summary.Raw), strconv.Itoa(summary.V2),
		strconv.Itoa(summary.Facets["election"]), strconv.Itoa(summary.Facets["replication"]),
		strconv.Itoa(summary.Facets["snapshot"]), strconv.Itoa(summary.Facets["recovery"]),
		strconv.Itoa(summary.Facets["network"]),
		strconv.Itoa(summary.Interactions["election_network"]),
		strconv.Itoa(summary.Interactions["replication_network"]),
		strconv.Itoa(summary.Interactions["snapshot_recovery"]),
		strconv.Itoa(summary.Interactions["recovery_term_relation"]),
		strconv.Itoa(summary.SemanticTraces),
	}
}

func coverageContains(left, right *coverageSets) bool {
	if left == nil || right == nil {
		return right == nil
	}
	if setDifferenceCount(right.raw, left.raw) != 0 ||
		setDifferenceCount(right.v2, left.v2) != 0 ||
		stringSetDifferenceCount(right.semanticTrace, left.semanticTrace) != 0 {
		return false
	}
	names := make([]string, 0, len(right.facets)+len(right.interactions))
	for name := range right.facets {
		names = append(names, "f:"+name)
	}
	for name := range right.interactions {
		names = append(names, "i:"+name)
	}
	sort.Strings(names)
	for _, tagged := range names {
		if tagged[:2] == "f:" {
			name := tagged[2:]
			if setDifferenceCount(right.facets[name], left.facets[name]) != 0 {
				return false
			}
		} else {
			name := tagged[2:]
			if setDifferenceCount(right.interactions[name], left.interactions[name]) != 0 {
				return false
			}
		}
	}
	return true
}
