package coverageanalysis

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// ObservationInput carries execution metadata that is intentionally separate
// from the protocol-neutral coverage guidance policy.
type ObservationInput struct {
	RunID         string
	CandidateID   string
	ParentPlanKey string
	Source        string
	Plan          plan.PlanSequence
	Result        engine.Result
	ModelConfig   raftmodel.Config
}

// BuildCoverageObservation is the one shared online/offline projection path.
// It delegates frame construction and all semantic classification to the
// frozen Raft coverage implementations.
func BuildCoverageObservation(input ObservationInput) (coverageguidance.CoverageObservation, error) {
	totalStarted := time.Now()
	observation := coverageguidance.CoverageObservation{
		RunID: input.RunID, CandidateID: input.CandidateID,
		ParentPlanKey:       input.ParentPlanKey,
		PlanKey:             goalsearch.PlanKey(input.Plan),
		TraceKey:            goalsearch.TraceKey(input.Result.Trace),
		ActionCount:         len(input.Result.Trace.Steps),
		ModelEventCount:     len(input.Result.ModelEvents),
		RawTLCFingerprints:  make([]coverageguidance.CoverageValue, 0),
		V2StateKeys:         make([]coverageguidance.CoverageValue, 0),
		FacetKeys:           make(map[string][]coverageguidance.CoverageValue),
		InteractionKeys:     make(map[string][]coverageguidance.CoverageValue),
		SemanticTraceDigest: goalsearch.SemanticTraceKey(input.Result.Trace),
		Outcome: coverageguidance.Outcome{
			Status: string(input.Result.Status), Succeeded: input.Result.Status == engine.StatusCompleted,
			RuntimeError: input.Result.Error, ModelExecuted: input.Result.ModelExecuted,
			ModelStateCount:    len(input.Result.ModelStates),
			OracleFindingCount: len(input.Result.OracleFindings),
		},
	}
	for _, name := range []string{"election", "replication", "snapshot", "recovery", "network"} {
		observation.FacetKeys[name] = make([]coverageguidance.CoverageValue, 0)
	}
	for _, name := range []string{
		"election_network", "replication_network", "snapshot_recovery", "recovery_term_relation",
	} {
		observation.InteractionKeys[name] = make([]coverageguidance.CoverageValue, 0)
	}
	if signature, failed := minimize.SignatureOf(input.Result); failed {
		encoded, _ := json.Marshal(signature)
		observation.Outcome.FailureSignature = string(encoded)
	}
	rawStarted := time.Now()
	for _, state := range input.Result.ModelStates {
		observation.RawTLCFingerprints = append(observation.RawTLCFingerprints,
			coverageguidance.CoverageValue{Key: state.Key, Value: state.Text})
	}
	observation.Computation.RawNanos = time.Since(rawStarted).Nanoseconds()
	v2Started := time.Now()
	for _, state := range input.Result.ModelStates {
		serialized, err := raftmodel.SerializeV2PrototypeState(state)
		if err != nil {
			return coverageguidance.CoverageObservation{}, fmt.Errorf("v2 observation projection: %w", err)
		}
		observation.V2StateKeys = append(observation.V2StateKeys,
			coverageguidance.CoverageValue{Key: raftmodel.StableCoverageKey(serialized), Value: serialized})
	}
	observation.Computation.V2Nanos = time.Since(v2Started).Nanoseconds()
	if len(input.Result.ModelStates) > 0 {
		frameStarted := time.Now()
		frames, err := BuildCoverageFrames(RunArtifact{
			Name: input.RunID, Source: input.Source, ModelConfig: input.ModelConfig,
			Initial: input.Result.Initial, Trace: input.Result.Trace,
			ModelEvents: input.Result.ModelEvents, ModelStates: input.Result.ModelStates,
		})
		if err != nil {
			return coverageguidance.CoverageObservation{}, err
		}
		observation.Computation.FrameNanos = time.Since(frameStarted).Nanoseconds()
		facetStarted := time.Now()
		for _, frame := range frames {
			projection, err := raftmodel.ProjectCoverageFacets(frame.ModelState, frame.Context)
			if err != nil {
				return coverageguidance.CoverageObservation{}, fmt.Errorf(
					"frame %d facet projection: %w", frame.ModelStateIndex, err)
			}
			readable, err := raftmodel.SerializeCoverageFacetProjection(projection)
			if err != nil {
				return coverageguidance.CoverageObservation{}, err
			}
			facetKeys := map[string]int64{
				"election": projection.ElectionKey, "replication": projection.ReplicationKey,
				"snapshot": projection.SnapshotKey, "recovery": projection.RecoveryKey,
				"network": projection.NetworkKey,
			}
			for _, name := range []string{"election", "replication", "snapshot", "recovery", "network"} {
				observation.FacetKeys[name] = append(observation.FacetKeys[name],
					coverageguidance.CoverageValue{Key: facetKeys[name], Value: readable[name]})
			}
			for _, interaction := range projection.Interactions {
				observation.InteractionKeys[interaction.Name] = append(
					observation.InteractionKeys[interaction.Name],
					coverageguidance.CoverageValue{Key: interaction.Key, Value: interaction.Value})
			}
		}
		observation.Computation.FacetNanos = time.Since(facetStarted).Nanoseconds()
	}
	observation.Computation.TotalNanos = time.Since(totalStarted).Nanoseconds()
	if err := coverageguidance.NormalizeObservation(&observation); err != nil {
		return coverageguidance.CoverageObservation{}, err
	}
	return observation, nil
}
