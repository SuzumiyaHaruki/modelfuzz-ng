package coverageguidance

import (
	"fmt"
	"math"
	"sort"
)

const SummarySchemaVersion = "coverage-guidance-summary-v1"

type GrowthPoint struct {
	Candidate         int   `json:"candidate"`
	CumulativeActions int   `json:"cumulative_actions"`
	ElapsedMillis     int64 `json:"elapsed_millis"`
	New               int   `json:"new"`
	Cumulative        int   `json:"cumulative"`
}

type QuartileNovelty struct {
	Quartile   int `json:"quartile"`
	Candidates int `json:"candidates"`
	NewUnits   int `json:"new_units"`
}

type DimensionSummary struct {
	Name                   string            `json:"name"`
	Distinct               int               `json:"distinct"`
	AUCByCandidate         float64           `json:"auc_by_candidate"`
	AUCByAction            float64           `json:"auc_by_action"`
	AUCByTime              float64           `json:"auc_by_time"`
	Quartiles              []QuartileNovelty `json:"quartiles"`
	Q4ToQ1                 *float64          `json:"q4_to_q1,omitempty"`
	LastNovelCandidate     int               `json:"last_novel_candidate"`
	NewPerThousandActions  float64           `json:"new_per_thousand_actions"`
	Singletons             int               `json:"singletons"`
	FrequentUnits          []CoverageValue   `json:"frequent_units"`
	RareUnits              []CoverageValue   `json:"rare_units"`
	ApproximatelySaturated bool              `json:"approximately_saturated"`
	Growth                 []GrowthPoint     `json:"growth"`
}

type CorpusSummary struct {
	Size                    int     `json:"size"`
	Admitted                int     `json:"admitted"`
	Rejected                int     `json:"rejected"`
	AdmissionRate           float64 `json:"admission_rate"`
	MultiFacetAdmissionRate float64 `json:"multi_facet_admission_rate"`
	SemanticDuplicateRatio  float64 `json:"semantic_duplicate_ratio"`
	RawNewFacetOld          int     `json:"raw_new_facet_old"`
	FacetNewRawOld          int     `json:"facet_new_raw_old"`
	V2NewFacetOld           int     `json:"v2_new_facet_old"`
	FacetNewV2Old           int     `json:"facet_new_v2_old"`
	ParentSelections        int     `json:"parent_selections"`
	ParentsWithNovelChild   int     `json:"parents_with_novel_child"`
	ParentNoveltyYield      float64 `json:"parent_novelty_yield"`
	CandidateLegalRate      float64 `json:"candidate_legal_rate"`
	CandidateExecutionRate  float64 `json:"candidate_execution_rate"`
	MeanExecutedActions     float64 `json:"mean_executed_actions"`
	MedianExecutedActions   float64 `json:"median_executed_actions"`
	NewUnitsPerAdmission    float64 `json:"new_units_per_admission"`
}

type ThroughputSummary struct {
	ElapsedMillis        int64   `json:"elapsed_millis"`
	CandidatesPerSecond  float64 `json:"candidates_per_second"`
	ActionsPerSecond     float64 `json:"actions_per_second"`
	ModelEventsPerSecond float64 `json:"model_events_per_second"`
	RawCoverageNanos     int64   `json:"raw_coverage_nanos"`
	V2CoverageNanos      int64   `json:"v2_coverage_nanos"`
	CoverageFrameNanos   int64   `json:"coverage_frame_nanos"`
	FacetCoverageNanos   int64   `json:"facet_coverage_nanos"`
	CorpusDecisionNanos  int64   `json:"corpus_decision_nanos"`
}

type Summary struct {
	Schema            string                      `json:"schema"`
	GuidanceSchema    string                      `json:"guidance_schema"`
	Mode              Mode                        `json:"mode"`
	Candidates        int                         `json:"candidates"`
	Actions           int                         `json:"actions"`
	ModelEvents       int                         `json:"model_events"`
	Dimensions        map[string]DimensionSummary `json:"dimensions"`
	Corpus            CorpusSummary               `json:"corpus"`
	Throughput        ThroughputSummary           `json:"throughput"`
	FailureCount      int                         `json:"failure_count"`
	FailureSignatures map[string]int              `json:"failure_signatures"`
	FinalCoverage     CoverageCounts              `json:"final_coverage"`
}

type CrossCoverageSummary struct {
	Schema                 string         `json:"schema"`
	Mode                   Mode           `json:"mode"`
	RawDistinct            int            `json:"raw_distinct"`
	V2Distinct             int            `json:"v2_distinct"`
	FacetDistinct          map[string]int `json:"facet_distinct"`
	InteractionDistinct    map[string]int `json:"interaction_distinct"`
	AllInteractionDistinct int            `json:"all_interaction_distinct"`
	CorpusSize             int            `json:"corpus_size"`
	SemanticTraceCount     int            `json:"semantic_trace_count"`
	GoalAReached           int            `json:"goal_a_reached"`
	GoalBReached           int            `json:"goal_b_reached"`
	Failures               int            `json:"failures"`
}

type dimensionAccumulator struct {
	values    map[int64]CoverageValue
	frequency map[int64]int
	growth    []GrowthPoint
	quartiles []QuartileNovelty
	last      int
	auc       float64
}

func Summarize(mode Mode, observations []CoverageObservation, decisions []Decision, elapsedMillis int64) (Summary, CrossCoverageSummary, error) {
	if len(observations) != len(decisions) {
		return Summary{}, CrossCoverageSummary{}, fmt.Errorf(
			"coverage observations=%d decisions=%d", len(observations), len(decisions))
	}
	names := append([]string{"raw", "v2"}, facetNames...)
	for _, name := range interactionNames {
		names = append(names, "interaction:"+name)
	}
	accumulators := make(map[string]*dimensionAccumulator, len(names))
	for _, name := range names {
		quartiles := make([]QuartileNovelty, 4)
		for index := range quartiles {
			quartiles[index].Quartile = index + 1
		}
		accumulators[name] = &dimensionAccumulator{
			values: make(map[int64]CoverageValue), frequency: make(map[int64]int),
			growth: make([]GrowthPoint, 0, len(observations)), quartiles: quartiles,
		}
	}
	semanticTraces := make(map[string]struct{})
	totalActions, totalEvents, failures := 0, 0, 0
	failureSignatures := make(map[string]int)
	admitted, multiFacet := 0, 0
	legal := 0
	totalAdmittedNewUnits := 0
	parentSelected := make(map[string]bool)
	parentNovel := make(map[string]bool)
	planLengths := make([]int, 0, len(observations))
	rawNewFacetOld, facetNewRawOld, v2NewFacetOld, facetNewV2Old := 0, 0, 0, 0
	for index, observation := range observations {
		if observation.StableKey == "" {
			if err := NormalizeObservation(&observation); err != nil {
				return Summary{}, CrossCoverageSummary{}, fmt.Errorf("observation %d: %w", index, err)
			}
		}
		if decisions[index].CandidateID != observation.CandidateID || decisions[index].GuidanceMode != mode {
			return Summary{}, CrossCoverageSummary{}, fmt.Errorf("decision %d does not match observation or mode", index)
		}
		totalActions += observation.ActionCount
		planLengths = append(planLengths, observation.ActionCount)
		totalEvents += observation.ModelEventCount
		if observation.Outcome.Status != "invalid_plan" &&
			observation.Outcome.Status != "resolution_failed" {
			legal++
		}
		if observation.SemanticTraceDigest != "" {
			semanticTraces[observation.SemanticTraceDigest] = struct{}{}
		}
		if !observation.Outcome.Succeeded {
			failures++
			if observation.Outcome.FailureSignature != "" {
				failureSignatures[observation.Outcome.FailureSignature]++
			}
		}
		decision := decisions[index]
		if decision.WasAdmitted {
			admitted++
			totalAdmittedNewUnits += noveltyUnitCount(decision.NewCoverageUnits)
			newFacetKinds := 0
			for _, name := range facetNames {
				if len(decision.NewCoverageUnits.Facets[name]) > 0 {
					newFacetKinds++
				}
			}
			if newFacetKinds > 1 {
				multiFacet++
			}
		}
		if observation.ParentPlanKey != "" {
			parentSelected[observation.ParentPlanKey] = true
			if noveltyUnitCount(decision.NewCoverageUnits) > 0 {
				parentNovel[observation.ParentPlanKey] = true
			}
		}
		facetNew := false
		for _, name := range facetNames {
			facetNew = facetNew || len(decision.NewCoverageUnits.Facets[name]) > 0
		}
		rawNew := len(decision.NewCoverageUnits.Raw) > 0
		v2New := len(decision.NewCoverageUnits.V2) > 0
		if rawNew && !facetNew {
			rawNewFacetOld++
		}
		if facetNew && !rawNew {
			facetNewRawOld++
		}
		if v2New && !facetNew {
			v2NewFacetOld++
		}
		if facetNew && !v2New {
			facetNewV2Old++
		}

		values := observationDimensions(observation)
		for _, name := range names {
			acc := accumulators[name]
			newCount := 0
			for _, value := range values[name] {
				acc.frequency[value.Key]++
				if _, found := acc.values[value.Key]; !found {
					acc.values[value.Key] = value
					newCount++
				}
			}
			if newCount > 0 {
				acc.last = index + 1
			}
			acc.auc += float64(len(acc.values))
			quartile := 0
			if len(observations) > 0 {
				quartile = index * 4 / len(observations)
				if quartile > 3 {
					quartile = 3
				}
			}
			acc.quartiles[quartile].Candidates++
			acc.quartiles[quartile].NewUnits += newCount
			pointElapsed := observation.ElapsedMillis
			if pointElapsed <= 0 && len(observations) > 0 {
				pointElapsed = elapsedMillis * int64(index+1) / int64(len(observations))
			}
			acc.growth = append(acc.growth, GrowthPoint{
				Candidate: index + 1, CumulativeActions: totalActions,
				ElapsedMillis: pointElapsed, New: newCount, Cumulative: len(acc.values),
			})
		}
	}
	dimensions := make(map[string]DimensionSummary, len(names))
	for _, name := range names {
		acc := accumulators[name]
		singletons := 0
		values := make([]CoverageValue, 0, len(acc.values))
		for key, value := range acc.values {
			values = append(values, value)
			if acc.frequency[key] == 1 {
				singletons++
			}
		}
		frequent := rankedValues(values, acc.frequency, true, 10)
		rare := rankedValues(values, acc.frequency, false, 10)
		q1, q4 := acc.quartiles[0].NewUnits, acc.quartiles[3].NewUnits
		var ratio *float64
		if q1 > 0 {
			value := float64(q4) / float64(q1)
			ratio = &value
		}
		newPerActions := 0.0
		if totalActions > 0 {
			newPerActions = float64(len(acc.values)) * 1000 / float64(totalActions)
		}
		auc := 0.0
		if len(observations) > 0 {
			auc = acc.auc / float64(len(observations))
		}
		dimensions[name] = DimensionSummary{
			Name: name, Distinct: len(acc.values), AUCByCandidate: auc,
			AUCByAction: normalizedGrowthAUC(acc.growth, func(point GrowthPoint) float64 {
				return float64(point.CumulativeActions)
			}),
			AUCByTime: normalizedGrowthAUC(acc.growth, func(point GrowthPoint) float64 {
				return float64(point.ElapsedMillis)
			}),
			Quartiles: acc.quartiles, Q4ToQ1: ratio, LastNovelCandidate: acc.last,
			NewPerThousandActions: newPerActions, Singletons: singletons,
			FrequentUnits: frequent, RareUnits: rare,
			ApproximatelySaturated: q1 > 0 && float64(q4)/float64(q1) <= 0.10,
			Growth:                 acc.growth,
		}
	}
	duplicateRatio := 0.0
	if admitted > 0 {
		admittedSemantics := make(map[string]struct{})
		for index, decision := range decisions {
			if decision.WasAdmitted {
				admittedSemantics[observations[index].SemanticTraceDigest] = struct{}{}
			}
		}
		duplicateRatio = float64(admitted-len(admittedSemantics)) / float64(admitted)
	}
	admissionRate, multiFacetRate := 0.0, 0.0
	if len(decisions) > 0 {
		admissionRate = float64(admitted) / float64(len(decisions))
	}
	if admitted > 0 {
		multiFacetRate = float64(multiFacet) / float64(admitted)
	}
	parentYield := 0.0
	if len(parentSelected) > 0 {
		parentYield = float64(len(parentNovel)) / float64(len(parentSelected))
	}
	legalRate := 0.0
	if len(observations) > 0 {
		legalRate = float64(legal) / float64(len(observations))
	}
	meanExecutedActions, medianExecutedActions := intDistribution(planLengths)
	newUnitsPerAdmission := 0.0
	if admitted > 0 {
		newUnitsPerAdmission = float64(totalAdmittedNewUnits) / float64(admitted)
	}
	seconds := float64(elapsedMillis) / 1000
	throughput := ThroughputSummary{ElapsedMillis: elapsedMillis}
	for _, observation := range observations {
		throughput.RawCoverageNanos += observation.Computation.RawNanos
		throughput.V2CoverageNanos += observation.Computation.V2Nanos
		throughput.CoverageFrameNanos += observation.Computation.FrameNanos
		throughput.FacetCoverageNanos += observation.Computation.FacetNanos
		throughput.CorpusDecisionNanos += observation.Computation.CorpusDecisionNanos
	}
	if seconds > 0 {
		throughput.CandidatesPerSecond = float64(len(observations)) / seconds
		throughput.ActionsPerSecond = float64(totalActions) / seconds
		throughput.ModelEventsPerSecond = float64(totalEvents) / seconds
	}
	finalCounts := CoverageCounts{
		Raw: dimensions["raw"].Distinct, V2: dimensions["v2"].Distinct,
		Facets: make(map[string]int), Interactions: make(map[string]int),
	}
	cross := CrossCoverageSummary{
		Schema: "cross-coverage-summary-v1", Mode: mode,
		RawDistinct: finalCounts.Raw, V2Distinct: finalCounts.V2,
		FacetDistinct: make(map[string]int), InteractionDistinct: make(map[string]int),
		CorpusSize: admitted, SemanticTraceCount: len(semanticTraces), Failures: failures,
	}
	for _, name := range facetNames {
		value := dimensions[name].Distinct
		finalCounts.Facets[name], cross.FacetDistinct[name] = value, value
	}
	for _, name := range interactionNames {
		value := dimensions["interaction:"+name].Distinct
		finalCounts.Interactions[name], cross.InteractionDistinct[name] = value, value
		cross.AllInteractionDistinct += value
	}
	summary := Summary{
		Schema: SummarySchemaVersion, GuidanceSchema: SchemaVersion, Mode: mode,
		Candidates: len(observations), Actions: totalActions, ModelEvents: totalEvents,
		Dimensions: dimensions,
		Corpus: CorpusSummary{
			Size: admitted, Admitted: admitted, Rejected: len(decisions) - admitted,
			AdmissionRate: admissionRate, MultiFacetAdmissionRate: multiFacetRate,
			SemanticDuplicateRatio: duplicateRatio,
			RawNewFacetOld:         rawNewFacetOld, FacetNewRawOld: facetNewRawOld,
			V2NewFacetOld: v2NewFacetOld, FacetNewV2Old: facetNewV2Old,
			ParentSelections: len(parentSelected), ParentsWithNovelChild: len(parentNovel),
			ParentNoveltyYield: parentYield, CandidateLegalRate: legalRate,
			CandidateExecutionRate: 1, MeanExecutedActions: meanExecutedActions,
			MedianExecutedActions: medianExecutedActions, NewUnitsPerAdmission: newUnitsPerAdmission,
		},
		Throughput: throughput, FailureCount: failures,
		FailureSignatures: failureSignatures, FinalCoverage: finalCounts,
	}
	return summary, cross, nil
}

func normalizedGrowthAUC(growth []GrowthPoint, x func(GrowthPoint) float64) float64 {
	if len(growth) == 0 {
		return 0
	}
	finalX := x(growth[len(growth)-1])
	if finalX <= 0 {
		return 0
	}
	area := 0.0
	previousX, previousY := 0.0, 0.0
	for _, point := range growth {
		currentX, currentY := x(point), float64(point.Cumulative)
		if currentX < previousX {
			currentX = previousX
		}
		area += (currentX - previousX) * (previousY + currentY) / 2
		previousX, previousY = currentX, currentY
	}
	return area / finalX
}

func noveltyUnitCount(novelty NoveltyVector) int {
	result := len(novelty.Raw) + len(novelty.V2)
	for _, values := range novelty.Facets {
		result += len(values)
	}
	for _, values := range novelty.Interactions {
		result += len(values)
	}
	return result
}

func intDistribution(values []int) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	total := 0
	for _, value := range sorted {
		total += value
	}
	median := float64(sorted[len(sorted)/2])
	if len(sorted)%2 == 0 {
		median = float64(sorted[len(sorted)/2-1]+sorted[len(sorted)/2]) / 2
	}
	return float64(total) / float64(len(sorted)), median
}

func observationDimensions(observation CoverageObservation) map[string][]CoverageValue {
	result := map[string][]CoverageValue{
		"raw": observation.RawTLCFingerprints, "v2": observation.V2StateKeys,
	}
	for _, name := range facetNames {
		result[name] = observation.FacetKeys[name]
	}
	for _, name := range interactionNames {
		result["interaction:"+name] = observation.InteractionKeys[name]
	}
	return result
}

func rankedValues(values []CoverageValue, frequency map[int64]int, descending bool, limit int) []CoverageValue {
	result := append([]CoverageValue(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		left, right := frequency[result[i].Key], frequency[result[j].Key]
		if left != right {
			if descending {
				return left > right
			}
			return left < right
		}
		return result[i].Key < result[j].Key
	})
	return result[:min(limit, len(result))]
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func standardDeviation(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	average := mean(values)
	total := 0.0
	for _, value := range values {
		total += math.Pow(value-average, 2)
	}
	return math.Sqrt(total / float64(len(values)-1))
}
