package facetbreadth

import (
	"fmt"
	"sort"
	"sync"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
)

const (
	CoverageStateSchemaIDV1  = "modelfuzz-ng-facet-breadth-state-v1"
	MaxCatalogKeysV1         = 31
	MaxRepresentativeSlotsV1 = 62
)

type DecisionReasonV1 string

const (
	DecisionNewFacetClass         DecisionReasonV1 = "new_facet_class"
	DecisionShorterRepresentative DecisionReasonV1 = "shorter_representative"
	DecisionNewAndShorter         DecisionReasonV1 = "new_and_shorter"
	DecisionNoNovelty             DecisionReasonV1 = "no_novelty"
	DecisionIneligibleEvidence    DecisionReasonV1 = "ineligible_evidence"
)

var decisionReasonOrder = []DecisionReasonV1{
	DecisionNewFacetClass,
	DecisionShorterRepresentative,
	DecisionNewAndShorter,
	DecisionNoNovelty,
	DecisionIneligibleEvidence,
}

type RepresentativeRefV1 struct {
	RecordDigest    string `json:"record_digest"`
	CandidateID     string `json:"candidate_id"`
	RunIndex        int    `json:"run_index"`
	PlanDigest      string `json:"plan_digest"`
	PlanActionCount int    `json:"plan_action_count"`
	TraceDigest     string `json:"trace_digest"`
	TraceStepCount  int    `json:"trace_step_count"`
	ApplyOrdinal    uint64 `json:"apply_ordinal"`
}

type DecisionV1 struct {
	Ordinal                 uint64           `json:"ordinal"`
	CandidateID             string           `json:"candidate_id"`
	RunIndex                int              `json:"run_index"`
	RecordDigest            string           `json:"record_digest"`
	PreCoveredCount         int              `json:"pre_covered_count"`
	PostCoveredCount        int              `json:"post_covered_count"`
	NewKeys                 []string         `json:"new_keys"`
	ShortestReplacementKeys []string         `json:"shortest_replacement_keys"`
	Admitted                bool             `json:"admitted"`
	Reason                  DecisionReasonV1 `json:"reason"`
}

type CoverageKeySnapshotV1 struct {
	Key             facet.KeyV1         `json:"key"`
	CanonicalString string              `json:"canonical_string"`
	KeyDigest       string              `json:"key_digest"`
	First           RepresentativeRefV1 `json:"first_representative"`
	Shortest        RepresentativeRefV1 `json:"shortest_representative"`
}

type DecisionReasonCountV1 struct {
	Reason DecisionReasonV1 `json:"reason"`
	Count  uint64           `json:"count"`
}

type CoverageSnapshotV1 struct {
	SchemaID              string                  `json:"schema_id"`
	MajorVersion          uint32                  `json:"major_version"`
	Catalog               CatalogIdentityV1       `json:"catalog"`
	Covered               []CoverageKeySnapshotV1 `json:"covered"`
	AppliedCandidateCount uint64                  `json:"applied_candidate_count"`
	EligibleCount         uint64                  `json:"eligible_count"`
	IneligibleCount       uint64                  `json:"ineligible_count"`
	DecisionReasonCounts  []DecisionReasonCountV1 `json:"decision_reason_counts"`
	NextApplyOrdinal      uint64                  `json:"next_apply_ordinal"`
	StateDigest           string                  `json:"state_digest"`
}

type coverageEntry struct {
	Key      facet.KeyV1
	Digest   string
	First    RepresentativeRefV1
	Shortest RepresentativeRefV1
}

type CoverageStateV1 struct {
	mu                    sync.RWMutex
	schemaID              string
	majorVersion          uint32
	catalog               CatalogIdentityV1
	covered               map[string]coverageEntry
	appliedCandidateCount uint64
	eligibleCount         uint64
	ineligibleCount       uint64
	reasonCounts          [5]uint64
	nextApplyOrdinal      uint64
}

type stateDigestPayload struct {
	SchemaID              string                  `json:"schema_id"`
	MajorVersion          uint32                  `json:"major_version"`
	CatalogFingerprint    string                  `json:"catalog_fingerprint"`
	Covered               []CoverageKeySnapshotV1 `json:"covered"`
	AppliedCandidateCount uint64                  `json:"applied_candidate_count"`
	EligibleCount         uint64                  `json:"eligible_count"`
	IneligibleCount       uint64                  `json:"ineligible_count"`
	DecisionReasonCounts  []DecisionReasonCountV1 `json:"decision_reason_counts"`
	NextApplyOrdinal      uint64                  `json:"next_apply_ordinal"`
}

func NewCoverageStateV1(catalog CatalogIdentityV1) (*CoverageStateV1, error) {
	if err := validateCatalogIdentity(catalog); err != nil {
		return nil, err
	}
	return &CoverageStateV1{
		schemaID: CoverageStateSchemaIDV1, majorVersion: MajorVersionV1,
		catalog: copyCatalog(catalog), covered: make(map[string]coverageEntry),
	}, nil
}

func (state *CoverageStateV1) Apply(
	ordinal uint64,
	summary CandidateFacetSummaryV1,
) (DecisionV1, error) {
	if state == nil {
		return DecisionV1{}, fmt.Errorf("coverage state is nil")
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	if ordinal != state.nextApplyOrdinal {
		return DecisionV1{}, fmt.Errorf("apply ordinal %d does not equal next ordinal %d", ordinal, state.nextApplyOrdinal)
	}
	if err := validateCandidateSummary(summary, state.catalog); err != nil {
		return DecisionV1{}, err
	}
	candidate := representativeFromSummary(summary, ordinal)
	if err := state.validateRepresentativeIdentity(candidate); err != nil {
		return DecisionV1{}, err
	}
	eligible := summaryEligible(summary)
	decision := DecisionV1{
		Ordinal: ordinal, CandidateID: summary.CandidateID, RunIndex: summary.RunIndex,
		RecordDigest: summary.RecordDigest, PreCoveredCount: len(state.covered),
		NewKeys: []string{}, ShortestReplacementKeys: []string{},
	}

	proposedCovered := cloneCovered(state.covered)
	proposedApplied := state.appliedCandidateCount + 1
	proposedEligible := state.eligibleCount
	proposedIneligible := state.ineligibleCount
	proposedReasons := state.reasonCounts
	if !eligible {
		decision.Reason = DecisionIneligibleEvidence
		decision.PostCoveredCount = len(proposedCovered)
		proposedIneligible++
		proposedReasons[reasonIndex(decision.Reason)]++
		state.commit(proposedCovered, proposedApplied, proposedEligible, proposedIneligible, proposedReasons)
		return copyDecision(decision), nil
	}

	keys := summaryKeys(summary)
	for _, key := range keys {
		existing, covered := proposedCovered[key.CanonicalString]
		if !covered {
			proposedCovered[key.CanonicalString] = coverageEntry{
				Key: key.Key, Digest: key.KeyDigest, First: candidate, Shortest: candidate,
			}
			decision.NewKeys = append(decision.NewKeys, key.CanonicalString)
			continue
		}
		if shorterRepresentative(candidate, existing.Shortest) {
			existing.Shortest = candidate
			proposedCovered[key.CanonicalString] = existing
			decision.ShortestReplacementKeys = append(
				decision.ShortestReplacementKeys, key.CanonicalString,
			)
		}
	}
	sort.Strings(decision.NewKeys)
	sort.Strings(decision.ShortestReplacementKeys)
	switch {
	case len(decision.NewKeys) > 0 && len(decision.ShortestReplacementKeys) > 0:
		decision.Reason = DecisionNewAndShorter
	case len(decision.NewKeys) > 0:
		decision.Reason = DecisionNewFacetClass
	case len(decision.ShortestReplacementKeys) > 0:
		decision.Reason = DecisionShorterRepresentative
	default:
		decision.Reason = DecisionNoNovelty
	}
	decision.Admitted = decision.Reason != DecisionNoNovelty
	decision.PostCoveredCount = len(proposedCovered)
	proposedEligible++
	proposedReasons[reasonIndex(decision.Reason)]++
	if err := validateProposedState(state.catalog, proposedCovered); err != nil {
		return DecisionV1{}, err
	}
	state.commit(proposedCovered, proposedApplied, proposedEligible, proposedIneligible, proposedReasons)
	return copyDecision(decision), nil
}

func (state *CoverageStateV1) Snapshot() CoverageSnapshotV1 {
	if state == nil {
		return CoverageSnapshotV1{}
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	snapshot, err := state.snapshotLocked()
	if err != nil {
		panic(err)
	}
	return snapshot
}

func (state *CoverageStateV1) Digest() (string, error) {
	if state == nil {
		return "", fmt.Errorf("coverage state is nil")
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	snapshot, err := state.snapshotLocked()
	if err != nil {
		return "", err
	}
	return snapshot.StateDigest, nil
}

func (state *CoverageStateV1) snapshotLocked() (CoverageSnapshotV1, error) {
	canonical := make([]string, 0, len(state.covered))
	for key := range state.covered {
		canonical = append(canonical, key)
	}
	sort.Strings(canonical)
	covered := make([]CoverageKeySnapshotV1, len(canonical))
	for index, key := range canonical {
		entry := state.covered[key]
		covered[index] = CoverageKeySnapshotV1{
			Key: entry.Key, CanonicalString: key, KeyDigest: entry.Digest,
			First: entry.First, Shortest: entry.Shortest,
		}
	}
	reasons := reasonCountsSlice(state.reasonCounts)
	payload := stateDigestPayload{
		SchemaID: state.schemaID, MajorVersion: state.majorVersion,
		CatalogFingerprint: state.catalog.Fingerprint, Covered: covered,
		AppliedCandidateCount: state.appliedCandidateCount,
		EligibleCount:         state.eligibleCount, IneligibleCount: state.ineligibleCount,
		DecisionReasonCounts: reasons, NextApplyOrdinal: state.nextApplyOrdinal,
	}
	digest, err := digestJSON(payload)
	if err != nil {
		return CoverageSnapshotV1{}, err
	}
	return CoverageSnapshotV1{
		SchemaID: state.schemaID, MajorVersion: state.majorVersion,
		Catalog: copyCatalog(state.catalog), Covered: append([]CoverageKeySnapshotV1(nil), covered...),
		AppliedCandidateCount: state.appliedCandidateCount,
		EligibleCount:         state.eligibleCount, IneligibleCount: state.ineligibleCount,
		DecisionReasonCounts: append([]DecisionReasonCountV1(nil), reasons...),
		NextApplyOrdinal:     state.nextApplyOrdinal, StateDigest: digest,
	}, nil
}

func (state *CoverageStateV1) validateRepresentativeIdentity(candidate RepresentativeRefV1) error {
	for _, entry := range state.covered {
		for _, existing := range []RepresentativeRefV1{entry.First, entry.Shortest} {
			if existing.RecordDigest == candidate.RecordDigest &&
				!sameRepresentativeIdentity(existing, candidate) {
				return fmt.Errorf("record digest %s has conflicting representative identity", candidate.RecordDigest)
			}
		}
	}
	return nil
}

func (state *CoverageStateV1) commit(
	covered map[string]coverageEntry,
	applied, eligible, ineligible uint64,
	reasons [5]uint64,
) {
	state.covered = covered
	state.appliedCandidateCount = applied
	state.eligibleCount = eligible
	state.ineligibleCount = ineligible
	state.reasonCounts = reasons
	state.nextApplyOrdinal++
}

func validateProposedState(catalog CatalogIdentityV1, covered map[string]coverageEntry) error {
	if len(covered) > MaxCatalogKeysV1 {
		return fmt.Errorf("coverage exceeds frozen 31-key catalog bound")
	}
	for canonical, entry := range covered {
		frozen, ok := frozenFacetByID(entry.Key.FacetID)
		if !ok {
			return fmt.Errorf("covered key is outside catalog")
		}
		if err := validateKeyForFrozen(entry.Key, frozen); err != nil {
			return err
		}
		actualCanonical, err := entry.Key.CanonicalString()
		if err != nil || actualCanonical != canonical {
			return fmt.Errorf("covered key canonical identity mismatch")
		}
		digest, err := entry.Key.Digest()
		if err != nil || digest != entry.Digest {
			return fmt.Errorf("covered key digest mismatch")
		}
		if err := validateRepresentative(entry.First); err != nil {
			return err
		}
		if err := validateRepresentative(entry.Shortest); err != nil {
			return err
		}
	}
	return validateCatalogIdentity(catalog)
}

func validateRepresentative(reference RepresentativeRefV1) error {
	if !validDigest(reference.RecordDigest) || !validDigest(reference.PlanDigest) ||
		!validDigest(reference.TraceDigest) || reference.CandidateID == "" ||
		reference.RunIndex < 0 || reference.PlanActionCount < 0 || reference.TraceStepCount < 0 {
		return fmt.Errorf("invalid representative reference")
	}
	return nil
}

func representativeFromSummary(summary CandidateFacetSummaryV1, ordinal uint64) RepresentativeRefV1 {
	return RepresentativeRefV1{
		RecordDigest: summary.RecordDigest, CandidateID: summary.CandidateID,
		RunIndex: summary.RunIndex, PlanDigest: summary.PlanDigest,
		PlanActionCount: summary.PlanActionCount, TraceDigest: summary.TraceDigest,
		TraceStepCount: summary.TraceStepCount, ApplyOrdinal: ordinal,
	}
}

func sameRepresentativeIdentity(left, right RepresentativeRefV1) bool {
	return left.RecordDigest == right.RecordDigest &&
		left.CandidateID == right.CandidateID &&
		left.RunIndex == right.RunIndex &&
		left.PlanDigest == right.PlanDigest &&
		left.PlanActionCount == right.PlanActionCount &&
		left.TraceDigest == right.TraceDigest &&
		left.TraceStepCount == right.TraceStepCount
}

func shorterRepresentative(candidate, current RepresentativeRefV1) bool {
	switch {
	case candidate.PlanActionCount != current.PlanActionCount:
		return candidate.PlanActionCount < current.PlanActionCount
	case candidate.TraceStepCount != current.TraceStepCount:
		return candidate.TraceStepCount < current.TraceStepCount
	case candidate.PlanDigest != current.PlanDigest:
		return candidate.PlanDigest < current.PlanDigest
	case candidate.TraceDigest != current.TraceDigest:
		return candidate.TraceDigest < current.TraceDigest
	case candidate.RecordDigest != current.RecordDigest:
		return candidate.RecordDigest < current.RecordDigest
	default:
		return false
	}
}

func summaryEligible(summary CandidateFacetSummaryV1) bool {
	totalKeys := 0
	for _, evaluation := range summary.Evaluations {
		totalKeys += len(evaluation.Keys)
		switch evaluation.FacetID {
		case "raft.election_role_term_shape", "raft.replication_alignment_shape":
			if evaluation.Status != facet.StatusEvaluated {
				return false
			}
		case "raft.snapshot_lifecycle_event":
			if evaluation.Status != facet.StatusEvaluated &&
				evaluation.Status != facet.StatusNotApplicable {
				return false
			}
		}
	}
	return totalKeys > 0
}

func summaryKeys(summary CandidateFacetSummaryV1) []FacetKeySummaryV1 {
	var result []FacetKeySummaryV1
	for _, evaluation := range summary.Evaluations {
		result = append(result, evaluation.Keys...)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CanonicalString < result[j].CanonicalString
	})
	return result
}

func cloneCovered(source map[string]coverageEntry) map[string]coverageEntry {
	result := make(map[string]coverageEntry, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func copyDecision(decision DecisionV1) DecisionV1 {
	result := decision
	result.NewKeys = append([]string(nil), decision.NewKeys...)
	result.ShortestReplacementKeys = append([]string(nil), decision.ShortestReplacementKeys...)
	return result
}

func reasonIndex(reason DecisionReasonV1) int {
	for index, candidate := range decisionReasonOrder {
		if candidate == reason {
			return index
		}
	}
	panic("invalid decision reason")
}

func reasonCountsSlice(counts [5]uint64) []DecisionReasonCountV1 {
	result := make([]DecisionReasonCountV1, len(decisionReasonOrder))
	for index, reason := range decisionReasonOrder {
		result[index] = DecisionReasonCountV1{Reason: reason, Count: counts[index]}
	}
	return result
}
