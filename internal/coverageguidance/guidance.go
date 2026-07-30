// Package coverageguidance provides protocol-neutral, deterministic corpus
// admission policies. Protocol-specific code is responsible for constructing
// CoverageObservation values; policies only compare stable coverage units.
package coverageguidance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

const SchemaVersion = "raft-online-coverage-guidance-v1-prototype"

type Mode string

const (
	ModeRandom                Mode = "random"
	ModeRawFixed              Mode = "raw-fixed"
	ModeV2Fixed               Mode = "v2-fixed"
	ModeFacetFixed            Mode = "facet-fixed"
	ModeFacetInteractionFixed Mode = "facet-interaction-fixed"
	ModeLegacyRaw             Mode = "legacy-raw"
)

var facetNames = []string{"election", "replication", "snapshot", "recovery", "network"}
var interactionNames = []string{
	"election_network", "replication_network", "snapshot_recovery", "recovery_term_relation",
}

func ParseMode(value string) (Mode, error) {
	mode := Mode(value)
	switch mode {
	case ModeRandom, ModeRawFixed, ModeV2Fixed, ModeFacetFixed,
		ModeFacetInteractionFixed, ModeLegacyRaw:
		return mode, nil
	default:
		return "", fmt.Errorf(
			"unknown coverage guidance mode %q; want random, raw-fixed, v2-fixed, facet-fixed, facet-interaction-fixed, or legacy-raw",
			value,
		)
	}
}

// CoverageValue retains both the stable comparison key and the readable,
// versioned semantic value from which that key was derived.
type CoverageValue struct {
	Key   int64  `json:"key"`
	Value string `json:"value"`
}

type Outcome struct {
	Status             string `json:"status"`
	Succeeded          bool   `json:"succeeded"`
	RuntimeError       string `json:"runtime_error,omitempty"`
	ModelExecuted      bool   `json:"model_executed"`
	ModelStateCount    int    `json:"model_state_count"`
	OracleFindingCount int    `json:"oracle_finding_count"`
	FailureSignature   string `json:"failure_signature,omitempty"`
}

type ComputationTiming struct {
	RawNanos            int64 `json:"raw_nanos"`
	V2Nanos             int64 `json:"v2_nanos"`
	FrameNanos          int64 `json:"coverage_frame_nanos"`
	FacetNanos          int64 `json:"facet_nanos"`
	TotalNanos          int64 `json:"total_nanos"`
	CorpusDecisionNanos int64 `json:"corpus_decision_nanos,omitempty"`
}

// CoverageObservation is the complete record-only view of one candidate.
// Facets and interactions remain independent collections; they are never
// concatenated into an artificial compound state.
type CoverageObservation struct {
	Schema              string                     `json:"schema"`
	RunID               string                     `json:"run_id"`
	CandidateID         string                     `json:"candidate_id"`
	ParentPlanKey       string                     `json:"parent_plan_key,omitempty"`
	PlanKey             string                     `json:"plan_key"`
	TraceKey            string                     `json:"trace_key"`
	ActionCount         int                        `json:"action_count"`
	ModelEventCount     int                        `json:"model_event_count"`
	ElapsedMillis       int64                      `json:"elapsed_millis,omitempty"`
	RawTLCFingerprints  []CoverageValue            `json:"raw_tlc_fingerprints"`
	V2StateKeys         []CoverageValue            `json:"v2_state_keys"`
	FacetKeys           map[string][]CoverageValue `json:"facet_keys"`
	InteractionKeys     map[string][]CoverageValue `json:"interaction_keys"`
	Outcome             Outcome                    `json:"outcome"`
	SemanticTraceDigest string                     `json:"semantic_trace_digest"`
	OfflineGoals        map[string]bool            `json:"offline_goals,omitempty"`
	Computation         ComputationTiming          `json:"computation"`
	StableKey           string                     `json:"stable_key"`
}

type NoveltyVector struct {
	Raw          []CoverageValue            `json:"raw"`
	V2           []CoverageValue            `json:"v2"`
	Facets       map[string][]CoverageValue `json:"facets"`
	Interactions map[string][]CoverageValue `json:"interactions"`
}

type CoverageCounts struct {
	Raw          int            `json:"raw"`
	V2           int            `json:"v2"`
	Facets       map[string]int `json:"facets"`
	Interactions map[string]int `json:"interactions"`
}

type Decision struct {
	Schema                string         `json:"schema"`
	GuidanceMode          Mode           `json:"guidance_mode"`
	CandidateID           string         `json:"candidate_id"`
	PlanKey               string         `json:"plan_key"`
	ParentPlanKey         string         `json:"parent_plan_key,omitempty"`
	CoverageUnitsObserved CoverageCounts `json:"coverage_units_observed"`
	NewCoverageUnits      NoveltyVector  `json:"new_coverage_units"`
	WasAdmitted           bool           `json:"was_admitted"`
	AdmissionReason       string         `json:"admission_reason"`
	CorpusSizeBefore      int            `json:"corpus_size_before"`
	CorpusSizeAfter       int            `json:"corpus_size_after"`
	FixedEnergy           int            `json:"fixed_energy"`
	StableDecisionKey     string         `json:"stable_decision_key"`
}

type ParentSelection struct {
	Schema        string `json:"schema"`
	Sequence      int    `json:"sequence"`
	CorpusID      string `json:"corpus_id"`
	ParentPlanKey string `json:"parent_plan_key"`
	Policy        string `json:"policy"`
	FixedEnergy   int    `json:"fixed_energy"`
}

type Config struct {
	Mode        Mode `json:"mode"`
	FixedEnergy int  `json:"fixed_energy"`
	CorpusLimit int  `json:"corpus_limit"`
}

// CoverageGuidance deliberately has no protocol vocabulary. Offline
// recomputation uses the same Observe operation over persisted observations.
type CoverageGuidance interface {
	Observe(CoverageObservation) (Decision, error)
	Snapshot() Snapshot
}

type Snapshot struct {
	Schema        string                     `json:"schema"`
	Config        Config                     `json:"config"`
	Raw           []CoverageValue            `json:"raw"`
	V2            []CoverageValue            `json:"v2"`
	Facets        map[string][]CoverageValue `json:"facets"`
	Interactions  map[string][]CoverageValue `json:"interactions"`
	AdmittedPlans []string                   `json:"admitted_plans"`
	Decisions     int                        `json:"decisions"`
}

type Controller struct {
	config       Config
	raw          map[int64]string
	v2           map[int64]string
	facets       map[string]map[int64]string
	interactions map[string]map[int64]string
	admitted     map[string]struct{}
	decisions    int
}

func New(config Config) (*Controller, error) {
	if _, err := ParseMode(string(config.Mode)); err != nil {
		return nil, err
	}
	if config.Mode == ModeLegacyRaw {
		return nil, fmt.Errorf("legacy-raw is handled by the frozen legacy corpus, not CoverageGuidance")
	}
	if config.FixedEnergy <= 0 {
		return nil, fmt.Errorf("fixed coverage guidance energy must be positive")
	}
	if config.CorpusLimit <= 0 {
		return nil, fmt.Errorf("coverage guidance corpus limit must be positive")
	}
	result := &Controller{
		config: config, raw: make(map[int64]string), v2: make(map[int64]string),
		facets: make(map[string]map[int64]string), interactions: make(map[string]map[int64]string),
		admitted: make(map[string]struct{}),
	}
	for _, name := range facetNames {
		result.facets[name] = make(map[int64]string)
	}
	for _, name := range interactionNames {
		result.interactions[name] = make(map[int64]string)
	}
	return result, nil
}

func (c *Controller) Observe(observation CoverageObservation) (Decision, error) {
	if c == nil {
		return Decision{}, fmt.Errorf("coverage guidance is nil")
	}
	if err := NormalizeObservation(&observation); err != nil {
		return Decision{}, err
	}
	before := len(c.admitted)
	// Random admission is intentionally decided without consulting novelty.
	wantsAdmission := c.config.Mode == ModeRandom
	novelty, err := c.mergeCoverage(observation)
	if err != nil {
		return Decision{}, err
	}
	switch c.config.Mode {
	case ModeRawFixed:
		wantsAdmission = len(novelty.Raw) > 0
	case ModeV2Fixed:
		wantsAdmission = len(novelty.V2) > 0
	case ModeFacetFixed:
		wantsAdmission = hasFacetNovelty(novelty)
	case ModeFacetInteractionFixed:
		wantsAdmission = hasFacetNovelty(novelty) || hasInteractionNovelty(novelty)
	}
	reason := c.noveltyReason(novelty, wantsAdmission)
	admitted := wantsAdmission
	if !observation.Outcome.Succeeded {
		admitted, reason = false, "rejected_unsuccessful_execution"
	} else if observation.PlanKey == "" {
		admitted, reason = false, "rejected_empty_plan_key"
	} else if _, duplicate := c.admitted[observation.PlanKey]; duplicate {
		admitted, reason = false, "rejected_duplicate_plan"
	} else if admitted && len(c.admitted) >= c.config.CorpusLimit {
		admitted, reason = false, "rejected_corpus_limit"
	}
	if admitted {
		c.admitted[observation.PlanKey] = struct{}{}
	}
	c.decisions++
	decision := Decision{
		Schema: SchemaVersion, GuidanceMode: c.config.Mode,
		CandidateID: observation.CandidateID, PlanKey: observation.PlanKey,
		ParentPlanKey:         observation.ParentPlanKey,
		CoverageUnitsObserved: counts(observation), NewCoverageUnits: novelty,
		WasAdmitted: admitted, AdmissionReason: reason,
		CorpusSizeBefore: before, CorpusSizeAfter: len(c.admitted),
		FixedEnergy: c.config.FixedEnergy,
	}
	decision.StableDecisionKey = stableDigest(struct {
		Schema      string
		Mode        Mode
		Observation string
		Decision    int
		Admitted    bool
		Reason      string
		Before      int
		After       int
		Energy      int
	}{SchemaVersion, c.config.Mode, observation.StableKey, c.decisions,
		decision.WasAdmitted, decision.AdmissionReason, before, len(c.admitted), c.config.FixedEnergy})
	return decision, nil
}

func (c *Controller) noveltyReason(n NoveltyVector, admitted bool) string {
	if c.config.Mode == ModeRandom {
		return "admitted_random_without_coverage"
	}
	if !admitted {
		return "rejected_no_guidance_novelty"
	}
	switch c.config.Mode {
	case ModeRawFixed:
		return "admitted_new_raw"
	case ModeV2Fixed:
		return "admitted_new_v2"
	case ModeFacetFixed:
		return "admitted_new_facet"
	case ModeFacetInteractionFixed:
		if hasFacetNovelty(n) && hasInteractionNovelty(n) {
			return "admitted_new_facet_and_interaction"
		}
		if hasFacetNovelty(n) {
			return "admitted_new_facet"
		}
		return "admitted_new_interaction"
	default:
		return "rejected_no_guidance_novelty"
	}
}

func (c *Controller) mergeCoverage(observation CoverageObservation) (NoveltyVector, error) {
	result := emptyNovelty()
	var err error
	if result.Raw, err = mergeValues(c.raw, observation.RawTLCFingerprints); err != nil {
		return NoveltyVector{}, fmt.Errorf("raw coverage: %w", err)
	}
	if result.V2, err = mergeValues(c.v2, observation.V2StateKeys); err != nil {
		return NoveltyVector{}, fmt.Errorf("v2 coverage: %w", err)
	}
	for _, name := range facetNames {
		result.Facets[name], err = mergeValues(c.facets[name], observation.FacetKeys[name])
		if err != nil {
			return NoveltyVector{}, fmt.Errorf("%s facet coverage: %w", name, err)
		}
	}
	for _, name := range interactionNames {
		result.Interactions[name], err = mergeValues(c.interactions[name], observation.InteractionKeys[name])
		if err != nil {
			return NoveltyVector{}, fmt.Errorf("%s interaction coverage: %w", name, err)
		}
	}
	return result, nil
}

func (c *Controller) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	result := Snapshot{
		Schema: SchemaVersion, Config: c.config, Raw: mapValues(c.raw), V2: mapValues(c.v2),
		Facets: make(map[string][]CoverageValue), Interactions: make(map[string][]CoverageValue),
		AdmittedPlans: make([]string, 0, len(c.admitted)), Decisions: c.decisions,
	}
	for _, name := range facetNames {
		result.Facets[name] = mapValues(c.facets[name])
	}
	for _, name := range interactionNames {
		result.Interactions[name] = mapValues(c.interactions[name])
	}
	for key := range c.admitted {
		result.AdmittedPlans = append(result.AdmittedPlans, key)
	}
	sort.Strings(result.AdmittedPlans)
	return result
}

func Recompute(config Config, observations []CoverageObservation) ([]Decision, Snapshot, error) {
	controller, err := New(config)
	if err != nil {
		return nil, Snapshot{}, err
	}
	decisions := make([]Decision, 0, len(observations))
	for index := range observations {
		decision, observeErr := controller.Observe(observations[index])
		if observeErr != nil {
			return nil, Snapshot{}, fmt.Errorf("observation %d: %w", index, observeErr)
		}
		decisions = append(decisions, decision)
	}
	return decisions, controller.Snapshot(), nil
}

func NormalizeObservation(observation *CoverageObservation) error {
	if observation == nil {
		return fmt.Errorf("coverage observation is nil")
	}
	if observation.RunID == "" || observation.CandidateID == "" {
		return fmt.Errorf("coverage observation requires run_id and candidate_id")
	}
	observation.Schema = SchemaVersion
	var err error
	if observation.RawTLCFingerprints, err = normalizeValues(observation.RawTLCFingerprints); err != nil {
		return err
	}
	if observation.V2StateKeys, err = normalizeValues(observation.V2StateKeys); err != nil {
		return err
	}
	if observation.FacetKeys == nil {
		observation.FacetKeys = make(map[string][]CoverageValue)
	}
	if observation.InteractionKeys == nil {
		observation.InteractionKeys = make(map[string][]CoverageValue)
	}
	for _, name := range facetNames {
		observation.FacetKeys[name], err = normalizeValues(observation.FacetKeys[name])
		if err != nil {
			return fmt.Errorf("%s facet: %w", name, err)
		}
	}
	for _, name := range interactionNames {
		observation.InteractionKeys[name], err = normalizeValues(observation.InteractionKeys[name])
		if err != nil {
			return fmt.Errorf("%s interaction: %w", name, err)
		}
	}
	observation.StableKey = ""
	timing := observation.Computation
	elapsed := observation.ElapsedMillis
	observation.Computation = ComputationTiming{}
	observation.ElapsedMillis = 0
	observation.StableKey = stableDigest(observation)
	observation.Computation = timing
	observation.ElapsedMillis = elapsed
	return nil
}

func emptyNovelty() NoveltyVector {
	result := NoveltyVector{
		Raw: make([]CoverageValue, 0), V2: make([]CoverageValue, 0),
		Facets: make(map[string][]CoverageValue), Interactions: make(map[string][]CoverageValue),
	}
	for _, name := range facetNames {
		result.Facets[name] = make([]CoverageValue, 0)
	}
	for _, name := range interactionNames {
		result.Interactions[name] = make([]CoverageValue, 0)
	}
	return result
}

func counts(observation CoverageObservation) CoverageCounts {
	result := CoverageCounts{
		Raw: len(observation.RawTLCFingerprints), V2: len(observation.V2StateKeys),
		Facets: make(map[string]int), Interactions: make(map[string]int),
	}
	for _, name := range facetNames {
		result.Facets[name] = len(observation.FacetKeys[name])
	}
	for _, name := range interactionNames {
		result.Interactions[name] = len(observation.InteractionKeys[name])
	}
	return result
}

func hasFacetNovelty(n NoveltyVector) bool {
	for _, name := range facetNames {
		if len(n.Facets[name]) > 0 {
			return true
		}
	}
	return false
}

func hasInteractionNovelty(n NoveltyVector) bool {
	for _, name := range interactionNames {
		if len(n.Interactions[name]) > 0 {
			return true
		}
	}
	return false
}

func mergeValues(seen map[int64]string, values []CoverageValue) ([]CoverageValue, error) {
	result := make([]CoverageValue, 0)
	for _, value := range values {
		if existing, found := seen[value.Key]; found {
			if existing != value.Value {
				return nil, fmt.Errorf("stable key collision for %d", value.Key)
			}
			continue
		}
		seen[value.Key] = value.Value
		result = append(result, value)
	}
	return result, nil
}

func normalizeValues(values []CoverageValue) ([]CoverageValue, error) {
	byKey := make(map[int64]string, len(values))
	for _, value := range values {
		if existing, found := byKey[value.Key]; found && existing != value.Value {
			return nil, fmt.Errorf("stable key collision for %d", value.Key)
		}
		byKey[value.Key] = value.Value
	}
	return mapValues(byKey), nil
}

func mapValues(values map[int64]string) []CoverageValue {
	result := make([]CoverageValue, 0, len(values))
	for key, value := range values {
		result = append(result, CoverageValue{Key: key, Value: value})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Key != result[j].Key {
			return result[i].Key < result[j].Key
		}
		return result[i].Value < result[j].Value
	})
	return result
}

func stableDigest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
