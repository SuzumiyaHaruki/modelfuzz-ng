// Package minimize reduces a failing high-level Plan while repeatedly executing
// it against the same deterministic runtime configuration.
package minimize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

var (
	ErrNotFailure      = errors.New("plan does not produce a reducible failure")
	ErrUnstableFailure = errors.New("plan failure signature is not deterministic")
)

type Config struct {
	MaxAttempts     int                    `json:"max_attempts"`
	VerifyRuns      int                    `json:"verify_runs"`
	FinalVerifyRuns int                    `json:"final_verify_runs"`
	Resume          *Checkpoint            `json:"-"`
	OnCheckpoint    func(Checkpoint) error `json:"-"`
	InputPlanSHA256 string                 `json:"-"`
	ConfigSHA256    string                 `json:"-"`
}

func DefaultConfig() Config {
	return Config{MaxAttempts: 1000, VerifyRuns: 2, FinalVerifyRuns: 3}
}

// Signature deliberately excludes action/step indexes: those indexes change as
// actions are deleted. Oracle codes and panic values remain exact.
type Signature struct {
	Status            engine.Status    `json:"status"`
	FailureKind       core.FailureKind `json:"failure_kind,omitempty"`
	FailureOperation  string           `json:"failure_operation,omitempty"`
	FailureCode       string           `json:"failure_code,omitempty"`
	PanicValue        string           `json:"panic_value,omitempty"`
	ModelErrorCode    string           `json:"model_error_code,omitempty"`
	ModelAction       string           `json:"model_action,omitempty"`
	MappingCode       string           `json:"mapping_code,omitempty"`
	RuntimeErrorClass string           `json:"runtime_error_class,omitempty"`
	OracleCodes       []string         `json:"oracle_codes,omitempty"`
	TerminationCode   string           `json:"termination_code,omitempty"`
}

func (s Signature) Equal(other Signature) bool {
	if s.Status != other.Status || s.FailureKind != other.FailureKind ||
		s.FailureOperation != other.FailureOperation || s.FailureCode != other.FailureCode ||
		s.PanicValue != other.PanicValue || s.ModelErrorCode != other.ModelErrorCode ||
		s.ModelAction != other.ModelAction || s.MappingCode != other.MappingCode ||
		s.RuntimeErrorClass != other.RuntimeErrorClass ||
		s.TerminationCode != other.TerminationCode || len(s.OracleCodes) != len(other.OracleCodes) {
		return false
	}
	for index := range s.OracleCodes {
		if s.OracleCodes[index] != other.OracleCodes[index] {
			return false
		}
	}
	return true
}

func SignatureOf(result engine.Result) (Signature, bool) {
	if result.Status == "" || result.Status == engine.StatusCompleted || result.Status == engine.StatusCanceled {
		return Signature{}, false
	}
	signature := Signature{Status: result.Status, TerminationCode: result.TerminationCode}
	if result.Failure != nil {
		signature.FailureKind = result.Failure.Kind
		signature.FailureOperation = result.Failure.Operation
		signature.PanicValue = result.Failure.PanicValue
	}
	classifyFailure(&signature, result)
	set := make(map[string]struct{}, len(result.OracleFindings))
	for _, finding := range result.OracleFindings {
		set[finding.Oracle+":"+finding.Code] = struct{}{}
	}
	for code := range set {
		signature.OracleCodes = append(signature.OracleCodes, code)
	}
	sort.Strings(signature.OracleCodes)
	return signature, true
}

var (
	tlcFailurePattern = regexp.MustCompile(`TLC ([a-zA-Z0-9_.-]+) at event [0-9]+ \(([^)]*)\)`)
	changingNumber    = regexp.MustCompile(`\b[0-9]+\b`)
	changingNode      = regexp.MustCompile(`\bn[0-9]+\b`)
	changingQuoted    = regexp.MustCompile(`"[^"]*"`)
)

func classifyFailure(signature *Signature, result engine.Result) {
	switch result.Status {
	case engine.StatusModelFailed:
		matches := tlcFailurePattern.FindStringSubmatch(result.Error)
		if len(matches) == 3 {
			signature.ModelErrorCode = matches[1]
			signature.ModelAction = matches[2]
			return
		}
		signature.ModelErrorCode = stableErrorClass(result.Error)
	case engine.StatusMappingFailed:
		signature.MappingCode = stableErrorClass(result.Error)
	case engine.StatusRuntimeFailed:
		if result.Failure != nil && result.Failure.Kind == core.FailureSUTPanic {
			return
		}
		if result.Failure != nil {
			signature.RuntimeErrorClass = stableErrorClass(result.Failure.Error)
		} else {
			signature.RuntimeErrorClass = stableErrorClass(result.Error)
		}
	case engine.StatusResolutionFailed:
		if len(result.Resolutions) > 0 {
			resolution := result.Resolutions[len(result.Resolutions)-1]
			signature.FailureCode = string(resolution.Status) + ":" + string(resolution.ReasonCode)
		} else {
			signature.FailureCode = stableErrorClass(result.Error)
		}
	case engine.StatusUnsupported:
		signature.FailureCode = result.TerminationCode
		if signature.FailureCode == "" {
			signature.FailureCode = stableErrorClass(result.Error)
		}
	case engine.StatusInvalidPlan, engine.StatusPolicyFailed:
		signature.FailureCode = stableErrorClass(result.Error)
	}
}

// stableErrorClass retains the semantic wording while removing values known to
// change when actions are deleted (indexes, node IDs and quoted payloads).
func stableErrorClass(message string) string {
	message = strings.TrimSpace(message)
	message = changingQuoted.ReplaceAllString(message, `"?"`)
	message = changingNode.ReplaceAllString(message, "n#")
	message = changingNumber.ReplaceAllString(message, "#")
	message = strings.Join(strings.Fields(message), " ")
	return message
}

type Execute func(context.Context, plan.PlanSequence) (engine.Result, error)

type Report struct {
	Signature           Signature `json:"signature"`
	OriginalActions     int       `json:"original_actions"`
	MinimizedActions    int       `json:"minimized_actions"`
	Attempts            int       `json:"attempts"`
	AcceptedReductions  int       `json:"accepted_reductions"`
	VerifyRuns          int       `json:"verify_runs"`
	FinalVerifyRuns     int       `json:"final_verify_runs"`
	StableReproductions int       `json:"stable_reproductions"`
	CacheHits           int       `json:"cache_hits"`
	OneMinimal          bool      `json:"one_minimal"`
	AttemptLimitReached bool      `json:"attempt_limit_reached"`
	InputPlanSHA256     string    `json:"input_plan_sha256,omitempty"`
	ConfigSHA256        string    `json:"config_sha256,omitempty"`
}

type Result struct {
	Plan               plan.PlanSequence `json:"plan"`
	BaselineExecution  engine.Result     `json:"baseline_execution"`
	MinimizedExecution engine.Result     `json:"minimized_execution"`
	Report             Report            `json:"report"`
}

const CheckpointVersion uint32 = 1

type Checkpoint struct {
	Version            uint32                     `json:"version"`
	OriginalPlan       plan.PlanSequence          `json:"original_plan"`
	CurrentPlan        plan.PlanSequence          `json:"current_plan"`
	BaselineExecution  engine.Result              `json:"baseline_execution"`
	CurrentExecution   engine.Result              `json:"current_execution"`
	Signature          Signature                  `json:"signature"`
	Attempts           int                        `json:"attempts"`
	AcceptedReductions int                        `json:"accepted_reductions"`
	VerifyRuns         int                        `json:"verify_runs"`
	FinalVerifyRuns    int                        `json:"final_verify_runs"`
	InputPlanSHA256    string                     `json:"input_plan_sha256"`
	ConfigSHA256       string                     `json:"config_sha256"`
	Cache              map[string]CachedExecution `json:"cache"`
	Complete           bool                       `json:"complete"`
}

type CachedExecution struct {
	Result    engine.Result `json:"result"`
	Signature Signature     `json:"signature"`
	Failed    bool          `json:"failed"`
}

func Reduce(ctx context.Context, sequence plan.PlanSequence, config Config, execute Execute) (Result, error) {
	if execute == nil {
		return Result{}, fmt.Errorf("minimize execute callback must not be nil")
	}
	if err := sequence.Validate(); err != nil {
		return Result{}, fmt.Errorf("invalid input plan: %w", err)
	}
	if config.FinalVerifyRuns == 0 {
		config.FinalVerifyRuns = 1
	}
	if config.MaxAttempts <= 0 || config.VerifyRuns <= 0 || config.FinalVerifyRuns <= 0 ||
		config.VerifyRuns > config.MaxAttempts || config.FinalVerifyRuns > config.MaxAttempts {
		return Result{}, fmt.Errorf("minimize attempts and verification runs must be positive and bounded")
	}
	state := reducer{config: config, execute: execute, cache: make(map[string]CachedExecution)}
	original := sequence.Copy()
	current := sequence.Copy()
	var baseline, currentExecution engine.Result
	var signature Signature
	if config.Resume != nil {
		checkpoint := config.Resume
		if checkpoint.Version != CheckpointVersion || checkpoint.Complete {
			return Result{}, fmt.Errorf("invalid or completed minimization checkpoint version %d", checkpoint.Version)
		}
		if checkpoint.VerifyRuns != config.VerifyRuns || checkpoint.FinalVerifyRuns != config.FinalVerifyRuns ||
			checkpoint.InputPlanSHA256 != config.InputPlanSHA256 || checkpoint.ConfigSHA256 != config.ConfigSHA256 {
			return Result{}, fmt.Errorf("minimization checkpoint configuration does not match")
		}
		if config.MaxAttempts <= checkpoint.Attempts {
			return Result{}, fmt.Errorf("minimize max attempts %d must exceed checkpoint attempts %d", config.MaxAttempts, checkpoint.Attempts)
		}
		original, current = checkpoint.OriginalPlan.Copy(), checkpoint.CurrentPlan.Copy()
		baseline, currentExecution, signature = checkpoint.BaselineExecution, checkpoint.CurrentExecution, checkpoint.Signature
		state.attempts, state.accepted = checkpoint.Attempts, checkpoint.AcceptedReductions
		for digest, cached := range checkpoint.Cache {
			state.cache[digest] = cached
		}
	} else {
		baselineExecution, baselineErr := state.run(ctx, sequence)
		baseline = baselineExecution
		if err := contextError(ctx, baselineErr); err != nil {
			return Result{}, err
		}
		if baselineErr != nil && baseline.Status == "" {
			return Result{}, baselineErr
		}
		var failed bool
		signature, failed = SignatureOf(baseline)
		if !failed {
			return Result{}, fmt.Errorf("%w: status=%s", ErrNotFailure, baseline.Status)
		}
		for verification := 1; verification < config.VerifyRuns; verification++ {
			repeated, err := state.run(ctx, sequence)
			if cancellation := contextError(ctx, err); cancellation != nil {
				return Result{}, cancellation
			}
			if err != nil && repeated.Status == "" {
				return Result{}, err
			}
			repeatedSignature, ok := SignatureOf(repeated)
			if !ok || !signature.Equal(repeatedSignature) {
				return Result{}, fmt.Errorf("%w: first=%s repeated=%s", ErrUnstableFailure,
					formatSignature(signature), formatSignature(repeatedSignature))
			}
		}
		currentExecution = baseline
		if err := state.saveState(original, current, baseline, currentExecution, signature, false); err != nil {
			return Result{}, err
		}
	}
	state.original, state.current = original.Copy(), current.Copy()
	state.baseline, state.currentExecution, state.signature = baseline, currentExecution, signature
	granularity := 2
	for len(current.Actions) > 0 && state.remaining() {
		if granularity > len(current.Actions) {
			granularity = len(current.Actions)
		}
		chunkSize := (len(current.Actions) + granularity - 1) / granularity
		reduced := false
		for start := 0; start < len(current.Actions) && state.remaining(); start += chunkSize {
			end := min(start+chunkSize, len(current.Actions))
			candidate := withoutRange(current, start, end)
			execution, reproduces, err := state.reproduces(ctx, candidate, signature)
			if err != nil {
				return Result{}, err
			}
			if reproduces {
				current, currentExecution = candidate, execution
				state.accepted++
				if err := state.saveState(original, current, baseline, currentExecution, signature, false); err != nil {
					return Result{}, err
				}
				granularity = max(2, granularity-1)
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		if granularity >= len(current.Actions) {
			break
		}
		granularity = min(len(current.Actions), granularity*2)
	}

	// A final single-action fixed-point pass makes the result 1-minimal for the
	// deletion operator even when ddmin arrived through uneven chunks.
	oneMinimal := state.remaining()
	for index := 0; index < len(current.Actions) && state.remaining(); {
		candidate := withoutRange(current, index, index+1)
		execution, reproduces, err := state.reproduces(ctx, candidate, signature)
		if err != nil {
			return Result{}, err
		}
		if reproduces {
			current, currentExecution = candidate, execution
			state.accepted++
			if err := state.saveState(original, current, baseline, currentExecution, signature, false); err != nil {
				return Result{}, err
			}
			index = 0
			continue
		}
		index++
	}
	if !state.remaining() {
		oneMinimal = false
	}
	stableReproductions := 1
	for stableReproductions < config.FinalVerifyRuns && state.remaining() {
		repeated, err := state.run(ctx, current)
		if cancellation := contextError(ctx, err); cancellation != nil {
			return Result{}, cancellation
		}
		if err != nil && repeated.Status == "" {
			return Result{}, err
		}
		repeatedSignature, ok := SignatureOf(repeated)
		if !ok || !signature.Equal(repeatedSignature) {
			return Result{}, fmt.Errorf("%w: minimized=%s repeated=%s", ErrUnstableFailure,
				formatSignature(signature), formatSignature(repeatedSignature))
		}
		currentExecution = repeated
		stableReproductions++
		if err := state.saveState(original, current, baseline, currentExecution, signature, false); err != nil {
			return Result{}, err
		}
	}
	result := Result{
		Plan: current, BaselineExecution: baseline, MinimizedExecution: currentExecution,
		Report: Report{
			Signature: signature, OriginalActions: len(original.Actions), MinimizedActions: len(current.Actions),
			Attempts: state.attempts, AcceptedReductions: state.accepted, VerifyRuns: config.VerifyRuns,
			FinalVerifyRuns: config.FinalVerifyRuns, StableReproductions: stableReproductions, CacheHits: state.cacheHits,
			OneMinimal: oneMinimal, AttemptLimitReached: !state.remaining(),
			InputPlanSHA256: config.InputPlanSHA256, ConfigSHA256: config.ConfigSHA256,
		},
	}
	if err := state.saveState(original, current, baseline, currentExecution, signature, true); err != nil {
		return Result{}, err
	}
	return result, nil
}

type reducer struct {
	config           Config
	execute          Execute
	attempts         int
	accepted         int
	cacheHits        int
	cache            map[string]CachedExecution
	original         plan.PlanSequence
	current          plan.PlanSequence
	baseline         engine.Result
	currentExecution engine.Result
	signature        Signature
}

func (r *reducer) remaining() bool { return r.attempts < r.config.MaxAttempts }

func (r *reducer) run(ctx context.Context, sequence plan.PlanSequence) (engine.Result, error) {
	if err := ctx.Err(); err != nil {
		return engine.Result{}, err
	}
	if !r.remaining() {
		return engine.Result{}, fmt.Errorf("minimize attempt limit reached")
	}
	r.attempts++
	return r.execute(ctx, sequence.Copy())
}

func (r *reducer) reproduces(ctx context.Context, sequence plan.PlanSequence, signature Signature) (engine.Result, bool, error) {
	if err := sequence.Validate(); err != nil {
		return engine.Result{}, false, nil
	}
	digest, err := planDigest(sequence)
	if err != nil {
		return engine.Result{}, false, err
	}
	if cached, ok := r.cache[digest]; ok {
		r.cacheHits++
		return cached.Result, cached.Failed && signature.Equal(cached.Signature), nil
	}
	result, err := r.run(ctx, sequence)
	if cancellation := contextError(ctx, err); cancellation != nil {
		return result, false, cancellation
	}
	if err != nil && result.Status == "" {
		return result, false, err
	}
	actual, failed := SignatureOf(result)
	r.cache[digest] = CachedExecution{Result: result, Signature: actual, Failed: failed}
	if err := r.save(false); err != nil {
		return result, false, err
	}
	return result, failed && signature.Equal(actual), nil
}

func (r *reducer) saveState(original, current plan.PlanSequence, baseline, execution engine.Result, signature Signature, complete bool) error {
	r.original, r.current = original.Copy(), current.Copy()
	r.baseline, r.currentExecution, r.signature = baseline, execution, signature
	return r.save(complete)
}

func (r *reducer) save(complete bool) error {
	if r.config.OnCheckpoint == nil {
		return nil
	}
	cache := make(map[string]CachedExecution, len(r.cache))
	for digest, cached := range r.cache {
		cache[digest] = cached
	}
	return r.config.OnCheckpoint(Checkpoint{
		Version: CheckpointVersion, OriginalPlan: r.original.Copy(), CurrentPlan: r.current.Copy(),
		BaselineExecution: r.baseline, CurrentExecution: r.currentExecution, Signature: r.signature,
		Attempts: r.attempts, AcceptedReductions: r.accepted, VerifyRuns: r.config.VerifyRuns,
		FinalVerifyRuns: r.config.FinalVerifyRuns, InputPlanSHA256: r.config.InputPlanSHA256,
		ConfigSHA256: r.config.ConfigSHA256, Cache: cache, Complete: complete,
	})
}

func planDigest(sequence plan.PlanSequence) (string, error) {
	data, err := json.Marshal(sequence.Actions)
	if err != nil {
		return "", fmt.Errorf("encode minimize candidate: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func contextError(ctx context.Context, executeErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded) {
		return executeErr
	}
	return nil
}

func withoutRange(sequence plan.PlanSequence, start, end int) plan.PlanSequence {
	result := sequence.Copy()
	result.Actions = make([]plan.PlanAction, 0, len(sequence.Actions)-(end-start))
	result.Actions = append(result.Actions, sequence.Actions[:start]...)
	result.Actions = append(result.Actions, sequence.Actions[end:]...)
	return result
}

func formatSignature(signature Signature) string {
	parts := []string{string(signature.Status)}
	if signature.FailureKind != "" {
		parts = append(parts, string(signature.FailureKind), signature.FailureOperation)
	}
	if signature.PanicValue != "" {
		parts = append(parts, signature.PanicValue)
	}
	for _, value := range []string{signature.FailureCode, signature.ModelErrorCode, signature.ModelAction,
		signature.MappingCode, signature.RuntimeErrorClass} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	parts = append(parts, signature.OracleCodes...)
	if signature.TerminationCode != "" {
		parts = append(parts, signature.TerminationCode)
	}
	return strings.Join(parts, "/")
}
