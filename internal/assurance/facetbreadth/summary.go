package facetbreadth

import (
	"fmt"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
)

const CandidateSummarySchemaIDV1 = "modelfuzz-ng-candidate-facet-summary-v1"

type FacetKeySummaryV1 struct {
	Key             facet.KeyV1      `json:"key"`
	CanonicalString string           `json:"canonical_string"`
	KeyDigest       string           `json:"key_digest"`
	FirstOccurrence facet.Occurrence `json:"first_occurrence"`
}

type FacetEvaluationSummaryV1 struct {
	FacetID      string                 `json:"facet_id"`
	FacetVersion uint32                 `json:"facet_version"`
	Status       facet.EvaluationStatus `json:"status"`
	Keys         []FacetKeySummaryV1    `json:"keys"`
}

type CandidateFacetSummaryV1 struct {
	SchemaID           string                     `json:"schema_id"`
	MajorVersion       uint32                     `json:"major_version"`
	SummaryDigest      string                     `json:"summary_digest"`
	CatalogFingerprint string                     `json:"catalog_fingerprint"`
	RecordDigest       string                     `json:"record_digest"`
	CandidateID        string                     `json:"candidate_id"`
	RunIndex           int                        `json:"run_index"`
	PlanDigest         string                     `json:"plan_digest"`
	PlanActionCount    int                        `json:"plan_action_count"`
	TraceDigest        string                     `json:"trace_digest"`
	TraceStepCount     int                        `json:"trace_step_count"`
	Evaluations        []FacetEvaluationSummaryV1 `json:"evaluations"`
}

type summaryDigestPayload struct {
	SchemaID           string                     `json:"schema_id"`
	MajorVersion       uint32                     `json:"major_version"`
	CatalogFingerprint string                     `json:"catalog_fingerprint"`
	RecordDigest       string                     `json:"record_digest"`
	CandidateID        string                     `json:"candidate_id"`
	RunIndex           int                        `json:"run_index"`
	PlanDigest         string                     `json:"plan_digest"`
	PlanActionCount    int                        `json:"plan_action_count"`
	TraceDigest        string                     `json:"trace_digest"`
	TraceStepCount     int                        `json:"trace_step_count"`
	Evaluations        []FacetEvaluationSummaryV1 `json:"evaluations"`
}

func BuildCandidateSummaryV1(
	record executionrecord.CompletedExecutionRecordV1,
	evaluations []facet.EvaluationV1,
) (CandidateFacetSummaryV1, error) {
	if record.SchemaID != executionrecord.SchemaIDV1 ||
		record.MajorVersion != executionrecord.MajorVersionV1 {
		return CandidateFacetSummaryV1{}, fmt.Errorf("unsupported completed execution record schema")
	}
	if !validDigest(record.RecordDigest) || !validDigest(record.Plan.Digest) ||
		!validDigest(record.Trace.Digest) {
		return CandidateFacetSummaryV1{}, fmt.Errorf("record contains malformed required digest")
	}
	if record.Candidate.ID == "" || record.Candidate.RunIndex < 0 ||
		record.Plan.ActionCount < 0 || record.Trace.StepCount < 0 {
		return CandidateFacetSummaryV1{}, fmt.Errorf("record contains invalid candidate identity or count")
	}
	catalog, err := frozenCatalogIdentity()
	if err != nil {
		return CandidateFacetSummaryV1{}, err
	}
	normalized, err := normalizeEvaluations(evaluations)
	if err != nil {
		return CandidateFacetSummaryV1{}, err
	}
	summary := CandidateFacetSummaryV1{
		SchemaID: CandidateSummarySchemaIDV1, MajorVersion: MajorVersionV1,
		CatalogFingerprint: catalog.Fingerprint,
		RecordDigest:       record.RecordDigest, CandidateID: record.Candidate.ID,
		RunIndex: record.Candidate.RunIndex, PlanDigest: record.Plan.Digest,
		PlanActionCount: record.Plan.ActionCount, TraceDigest: record.Trace.Digest,
		TraceStepCount: record.Trace.StepCount, Evaluations: normalized,
	}
	digest, err := summaryDigest(summary)
	if err != nil {
		return CandidateFacetSummaryV1{}, err
	}
	summary.SummaryDigest = digest
	if err := validateCandidateSummary(summary, catalog); err != nil {
		return CandidateFacetSummaryV1{}, err
	}
	return copySummary(summary), nil
}

func normalizeEvaluations(evaluations []facet.EvaluationV1) ([]FacetEvaluationSummaryV1, error) {
	if len(evaluations) != len(frozenCatalogV1) {
		return nil, fmt.Errorf("candidate summary requires exactly three evaluations")
	}
	result := make([]FacetEvaluationSummaryV1, 0, len(evaluations))
	seen := make(map[string]struct{}, len(evaluations))
	for _, evaluation := range evaluations {
		frozen, ok := frozenFacetByID(evaluation.FacetID)
		if !ok || evaluation.FacetVersion != frozen.Version {
			return nil, fmt.Errorf("unknown facet evaluation %s v%d", evaluation.FacetID, evaluation.FacetVersion)
		}
		identity := fmt.Sprintf("%s\x00%d", evaluation.FacetID, evaluation.FacetVersion)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("duplicate facet evaluation %s v%d", evaluation.FacetID, evaluation.FacetVersion)
		}
		seen[identity] = struct{}{}
		if !evaluation.Status.Valid() {
			return nil, fmt.Errorf("facet %s has invalid evaluation status", evaluation.FacetID)
		}
		if evaluation.Status != facet.StatusEvaluated && len(evaluation.Observations) != 0 {
			return nil, fmt.Errorf("non-evaluated facet %s has observations", evaluation.FacetID)
		}
		if evaluation.Status == facet.StatusEvaluated && len(evaluation.Observations) == 0 {
			return nil, fmt.Errorf("evaluated facet %s has no observations", evaluation.FacetID)
		}
		summary := FacetEvaluationSummaryV1{
			FacetID: evaluation.FacetID, FacetVersion: evaluation.FacetVersion,
			Status: evaluation.Status, Keys: []FacetKeySummaryV1{},
		}
		if evaluation.Status == facet.StatusEvaluated {
			keys, err := normalizeObservations(frozen, evaluation.Observations)
			if err != nil {
				return nil, fmt.Errorf("facet %s: %w", evaluation.FacetID, err)
			}
			summary.Keys = keys
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].FacetID != result[j].FacetID {
			return result[i].FacetID < result[j].FacetID
		}
		return result[i].FacetVersion < result[j].FacetVersion
	})
	return result, nil
}

func normalizeObservations(frozen frozenFacet, observations []facet.ObservationV1) ([]FacetKeySummaryV1, error) {
	byCanonical := make(map[string]FacetKeySummaryV1, len(observations))
	for _, observation := range observations {
		if err := validateKeyForFrozen(observation.Key, frozen); err != nil {
			return nil, err
		}
		if err := observation.Occurrence.Validate(); err != nil {
			return nil, err
		}
		canonical, err := observation.Key.CanonicalString()
		if err != nil {
			return nil, err
		}
		digest, err := observation.Key.Digest()
		if err != nil || digest != observation.KeyDigest {
			return nil, fmt.Errorf("key %s digest is inconsistent", canonical)
		}
		candidate := FacetKeySummaryV1{
			Key: observation.Key, CanonicalString: canonical, KeyDigest: digest,
			FirstOccurrence: observation.Occurrence.Copy(),
		}
		existing, duplicate := byCanonical[canonical]
		if !duplicate {
			byCanonical[canonical] = candidate
			continue
		}
		if existing.Key != candidate.Key || existing.KeyDigest != candidate.KeyDigest {
			return nil, fmt.Errorf("key %s has contradictory duplicate observations", canonical)
		}
		if occurrenceLess(candidate.FirstOccurrence, existing.FirstOccurrence) {
			byCanonical[canonical] = candidate
		}
	}
	canonicalKeys := make([]string, 0, len(byCanonical))
	for canonical := range byCanonical {
		canonicalKeys = append(canonicalKeys, canonical)
	}
	sort.Strings(canonicalKeys)
	result := make([]FacetKeySummaryV1, len(canonicalKeys))
	for index, canonical := range canonicalKeys {
		result[index] = copyKeySummary(byCanonical[canonical])
	}
	return result, nil
}

func validateCandidateSummary(summary CandidateFacetSummaryV1, catalog CatalogIdentityV1) error {
	if summary.SchemaID != CandidateSummarySchemaIDV1 || summary.MajorVersion != MajorVersionV1 {
		return fmt.Errorf("unsupported candidate facet summary schema")
	}
	if err := validateCatalogIdentity(catalog); err != nil {
		return err
	}
	if summary.CatalogFingerprint != catalog.Fingerprint {
		return fmt.Errorf("candidate summary catalog fingerprint mismatch")
	}
	if !validDigest(summary.RecordDigest) || !validDigest(summary.PlanDigest) ||
		!validDigest(summary.TraceDigest) || !validDigest(summary.SummaryDigest) {
		return fmt.Errorf("candidate summary contains malformed digest")
	}
	if summary.CandidateID == "" || summary.RunIndex < 0 ||
		summary.PlanActionCount < 0 || summary.TraceStepCount < 0 {
		return fmt.Errorf("candidate summary contains invalid identity or count")
	}
	if len(summary.Evaluations) != len(frozenCatalogV1) {
		return fmt.Errorf("candidate summary must contain exactly three evaluations")
	}
	for index, evaluation := range summary.Evaluations {
		frozen := frozenCatalogV1[index]
		if evaluation.FacetID != frozen.ID || evaluation.FacetVersion != frozen.Version ||
			!evaluation.Status.Valid() {
			return fmt.Errorf("candidate evaluation identity, order, or status is invalid")
		}
		if evaluation.Status == facet.StatusEvaluated && len(evaluation.Keys) == 0 {
			return fmt.Errorf("evaluated facet %s has no keys", evaluation.FacetID)
		}
		if evaluation.Status != facet.StatusEvaluated && len(evaluation.Keys) != 0 {
			return fmt.Errorf("non-evaluated facet %s has keys", evaluation.FacetID)
		}
		previous := ""
		for _, key := range evaluation.Keys {
			if err := validateKeySummary(key, frozen); err != nil {
				return err
			}
			if previous != "" && previous >= key.CanonicalString {
				return fmt.Errorf("facet keys are not canonical and unique")
			}
			previous = key.CanonicalString
		}
	}
	digest, err := summaryDigest(summary)
	if err != nil || digest != summary.SummaryDigest {
		return fmt.Errorf("candidate summary digest mismatch")
	}
	return nil
}

func validateKeySummary(summary FacetKeySummaryV1, frozen frozenFacet) error {
	if err := validateKeyForFrozen(summary.Key, frozen); err != nil {
		return err
	}
	if err := summary.FirstOccurrence.Validate(); err != nil {
		return err
	}
	canonical, err := summary.Key.CanonicalString()
	if err != nil || canonical != summary.CanonicalString {
		return fmt.Errorf("facet key canonical string mismatch")
	}
	digest, err := summary.Key.Digest()
	if err != nil || digest != summary.KeyDigest {
		return fmt.Errorf("facet key digest mismatch")
	}
	return nil
}

func validateKeyForFrozen(key facet.KeyV1, frozen frozenFacet) error {
	if key.SchemaID != facet.KeySchemaIDV1 || key.FacetID != frozen.ID ||
		key.FacetVersion != frozen.Version || key.Scope != frozen.Scope ||
		!containsString(frozen.Classes, key.ClassID) {
		return fmt.Errorf("facet key is outside frozen catalog v1")
	}
	return nil
}

func summaryDigest(summary CandidateFacetSummaryV1) (string, error) {
	return digestJSON(summaryDigestPayload{
		SchemaID: summary.SchemaID, MajorVersion: summary.MajorVersion,
		CatalogFingerprint: summary.CatalogFingerprint, RecordDigest: summary.RecordDigest,
		CandidateID: summary.CandidateID, RunIndex: summary.RunIndex,
		PlanDigest: summary.PlanDigest, PlanActionCount: summary.PlanActionCount,
		TraceDigest: summary.TraceDigest, TraceStepCount: summary.TraceStepCount,
		Evaluations: copyEvaluations(summary.Evaluations),
	})
}

func frozenCatalogIdentity() (CatalogIdentityV1, error) {
	identities := make([]CatalogFacetIdentityV1, len(frozenCatalogV1))
	for index, frozen := range frozenCatalogV1 {
		classIDs := append([]string(nil), frozen.Classes...)
		digest, err := digestJSON(classSetDigestPayload{
			FacetID: frozen.ID, FacetVersion: frozen.Version, Scope: frozen.Scope, ClassIDs: classIDs,
		})
		if err != nil {
			return CatalogIdentityV1{}, err
		}
		identities[index] = CatalogFacetIdentityV1{
			FacetID: frozen.ID, FacetVersion: frozen.Version, Scope: frozen.Scope,
			ClassIDs: classIDs, ClassSetDigest: digest,
		}
	}
	catalog := CatalogIdentityV1{
		SchemaID: CatalogIdentitySchemaIDV1, MajorVersion: MajorVersionV1, Facets: identities,
	}
	fingerprint, err := catalogFingerprint(catalog)
	if err != nil {
		return CatalogIdentityV1{}, err
	}
	catalog.Fingerprint = fingerprint
	return catalog, nil
}

func copySummary(summary CandidateFacetSummaryV1) CandidateFacetSummaryV1 {
	result := summary
	result.Evaluations = copyEvaluations(summary.Evaluations)
	return result
}

func copyEvaluations(evaluations []FacetEvaluationSummaryV1) []FacetEvaluationSummaryV1 {
	result := make([]FacetEvaluationSummaryV1, len(evaluations))
	for index, evaluation := range evaluations {
		result[index] = evaluation
		result[index].Keys = make([]FacetKeySummaryV1, len(evaluation.Keys))
		for keyIndex, key := range evaluation.Keys {
			result[index].Keys[keyIndex] = copyKeySummary(key)
		}
	}
	return result
}

func copyKeySummary(summary FacetKeySummaryV1) FacetKeySummaryV1 {
	result := summary
	result.FirstOccurrence = summary.FirstOccurrence.Copy()
	return result
}

func occurrenceLess(left, right facet.Occurrence) bool {
	leftStep, leftEffect, leftRank := occurrenceOrder(left)
	rightStep, rightEffect, rightRank := occurrenceOrder(right)
	if leftStep != rightStep {
		return leftStep < rightStep
	}
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	return leftEffect < rightEffect
}

func occurrenceOrder(occurrence facet.Occurrence) (step, effect, rank int) {
	switch occurrence.Kind {
	case facet.OccurrenceExplicitInitial:
		return -1, -1, 0
	case facet.OccurrenceTraceInitialBefore:
		return *occurrence.StepIndex, -1, 0
	case facet.OccurrenceTransitionEffect:
		return *occurrence.StepIndex, *occurrence.EffectIndex, 1
	case facet.OccurrenceTraceStepAfter:
		return *occurrence.StepIndex, -1, 2
	default:
		return 0, 0, 3
	}
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}
