package executionrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestBuildV1FromCompletion(t *testing.T) {
	input := validBuildInput()
	record, err := BuildV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if record.SchemaID != SchemaIDV1 || record.MajorVersion != MajorVersionV1 || !validSHA256(record.RecordDigest) {
		t.Fatalf("schema/digest = %q/%d/%q", record.SchemaID, record.MajorVersion, record.RecordDigest)
	}
	if record.Candidate.ID != "candidate-000001" || record.Candidate.Kind != experiment.CandidateInitial ||
		record.Candidate.RunIndex != 1 || record.Candidate.Seed != 42 {
		t.Fatalf("candidate = %+v", record.Candidate)
	}
	if record.Plan.Digest != digestOf('a') || record.Plan.ActionCount != 1 ||
		record.Plan.Artifact == nil || record.Plan.Artifact.Kind != ArtifactPlan {
		t.Fatalf("plan = %+v", record.Plan)
	}
	if record.Engine.Status != engine.StatusCompleted || !record.Engine.ModelExecuted ||
		record.Engine.ResolutionCount != 1 || record.Engine.ModelEventCount != 1 ||
		record.Engine.ModelStateCount != 2 || record.Engine.OracleFindingCount != 3 {
		t.Fatalf("engine outcome = %+v", record.Engine)
	}
	if !record.Experiment.Completed || record.Experiment.Status != engine.StatusCompleted ||
		!record.Experiment.Succeeded {
		t.Fatalf("experiment outcome = %+v", record.Experiment)
	}
	if record.Trace.Digest != digestOf('b') || record.Trace.ExecutionID != "execution-42" ||
		record.Trace.Seed != 42 || record.Trace.Artifact == nil {
		t.Fatalf("trace = %+v", record.Trace)
	}
	if record.Model.StatePathDigest != digestOf('c') || record.Model.EventsArtifact == nil ||
		record.Model.StatesArtifact == nil {
		t.Fatalf("model = %+v", record.Model)
	}
	wantCodes := []string{"raft-agreement:leader", "raft-log:commit"}
	if !reflect.DeepEqual(record.Oracle.Codes, wantCodes) || record.Oracle.Artifact == nil {
		t.Fatalf("oracle = %+v", record.Oracle)
	}
	if record.Failure.Availability != FailureSignatureNotApplicable || record.Failure.Signature != nil {
		t.Fatalf("failure = %+v", record.Failure)
	}
	if !record.Corpus.Retained || record.Corpus.ID != "corpus-000001" ||
		record.Corpus.Admission != "retained_raw" {
		t.Fatalf("corpus = %+v", record.Corpus)
	}
	if !record.Replay.Replayable || record.Replay.ConfigArtifact == nil || record.Replay.TraceArtifact == nil {
		t.Fatalf("replay = %+v", record.Replay)
	}
	for i := 1; i < len(record.Artifacts); i++ {
		left, right := record.Artifacts[i-1], record.Artifacts[i]
		if left.Kind > right.Kind || left.Kind == right.Kind && left.Path > right.Path {
			t.Fatalf("artifacts are not canonical: %+v", record.Artifacts)
		}
	}
}

func TestBuildV1PreservesEngineAndExperimentOutcomeDivergence(t *testing.T) {
	input := validBuildInput()
	input.Completion.Execution.Result.Status = engine.StatusCompleted
	input.Completion.Run.Status = engine.StatusMappingFailed
	input.Completion.Run.Succeeded = false
	input.Completion.Run.Error = "projection failed after engine completion"

	record, err := BuildV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if record.Engine.Status != engine.StatusCompleted || record.Experiment.Status != engine.StatusMappingFailed ||
		record.Experiment.Succeeded {
		t.Fatalf("outcomes were conflated: engine=%+v experiment=%+v", record.Engine, record.Experiment)
	}
}

func TestBuildV1PreservesCurrentEngineStatuses(t *testing.T) {
	statuses := []engine.Status{
		engine.StatusCompleted,
		engine.StatusCanceled,
		engine.StatusInvalidPlan,
		engine.StatusResolutionFailed,
		engine.StatusRuntimeFailed,
		engine.StatusMappingFailed,
		engine.StatusUnsupported,
		engine.StatusOracleFailed,
		engine.StatusPolicyFailed,
		engine.StatusModelFailed,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			input := validBuildInput()
			input.Completion.Execution.Result.Status = status
			record, err := BuildV1(input)
			if err != nil {
				t.Fatal(err)
			}
			if record.Engine.Status != status {
				t.Fatalf("status = %q, want %q", record.Engine.Status, status)
			}
		})
	}
}

func TestBuildV1PreservesModelExecutedOnModelFailure(t *testing.T) {
	input := validBuildInput()
	input.Completion.Execution.Result.Status = engine.StatusModelFailed
	input.Completion.Execution.Result.ModelExecuted = true
	record, err := BuildV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if record.Engine.Status != engine.StatusModelFailed || !record.Engine.ModelExecuted || !record.Model.Executed {
		t.Fatalf("model failure lost executed state: engine=%+v model=%+v", record.Engine, record.Model)
	}
}

func TestBuildV1ModelAndOracleSummary(t *testing.T) {
	record, err := BuildV1(validBuildInput())
	if err != nil {
		t.Fatal(err)
	}
	if record.Model.EventCount != 1 || record.Model.StateCount != 2 ||
		record.Model.StatePathDigest != digestOf('c') {
		t.Fatalf("model summary = %+v", record.Model)
	}
	want := []string{"raft-agreement:leader", "raft-log:commit"}
	if record.Oracle.FindingCount != 3 || !reflect.DeepEqual(record.Oracle.Codes, want) {
		t.Fatalf("oracle summary = %+v", record.Oracle)
	}
}

func TestBuildV1FailureSignatureAvailability(t *testing.T) {
	signature := minimize.Signature{
		Status: engine.StatusOracleFailed, FailureCode: "stable",
		OracleCodes: []string{"z:two", "a:one", "z:two"},
	}
	tests := []struct {
		name       string
		input      FailureSignatureInput
		wantErr    bool
		wantDigest bool
	}{
		{name: "not-applicable", input: FailureSignatureInput{Availability: FailureSignatureNotApplicable}},
		{name: "unavailable", input: FailureSignatureInput{Availability: FailureSignatureUnavailable}},
		{name: "available", input: FailureSignatureInput{Availability: FailureSignatureAvailable, Signature: &signature}, wantDigest: true},
		{name: "not-applicable-with-pointer", input: FailureSignatureInput{Availability: FailureSignatureNotApplicable, Signature: &signature}, wantErr: true},
		{name: "unavailable-with-pointer", input: FailureSignatureInput{Availability: FailureSignatureUnavailable, Signature: &signature}, wantErr: true},
		{name: "available-without-pointer", input: FailureSignatureInput{Availability: FailureSignatureAvailable}, wantErr: true},
		{name: "unknown", input: FailureSignatureInput{Availability: "unknown"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validBuildInput()
			input.FailureSignature = test.input
			if test.input.Availability != FailureSignatureNotApplicable {
				input.Completion.Execution.Result.Status = engine.StatusRuntimeFailed
			}
			record, err := BuildV1(input)
			if test.wantErr {
				if err == nil {
					t.Fatal("BuildV1 unexpectedly succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (record.Failure.SignatureDigest != "") != test.wantDigest {
				t.Fatalf("signature digest = %q", record.Failure.SignatureDigest)
			}
			if test.wantDigest {
				want := []string{"a:one", "z:two"}
				if record.Failure.Signature == nil ||
					!reflect.DeepEqual(record.Failure.Signature.OracleCodes, want) {
					t.Fatalf("signature = %+v", record.Failure.Signature)
				}
				signature.OracleCodes[0] = "mutated"
				if !reflect.DeepEqual(record.Failure.Signature.OracleCodes, want) {
					t.Fatalf("record signature shared caller memory: %+v", record.Failure.Signature)
				}
			}
		})
	}
}

func TestArtifactReferenceValidation(t *testing.T) {
	valid := ArtifactReference{Kind: ArtifactTrace, Path: "runs/000001/trace.json", SHA256: digestOf('a')}
	tests := []struct {
		name string
		ref  ArtifactReference
	}{
		{name: "unknown-kind", ref: ArtifactReference{Kind: "unknown", Path: valid.Path, SHA256: valid.SHA256}},
		{name: "empty-path", ref: ArtifactReference{Kind: ArtifactTrace, SHA256: valid.SHA256}},
		{name: "absolute", ref: ArtifactReference{Kind: ArtifactTrace, Path: "/tmp/trace.json", SHA256: valid.SHA256}},
		{name: "escape", ref: ArtifactReference{Kind: ArtifactTrace, Path: "../trace.json", SHA256: valid.SHA256}},
		{name: "embedded-escape", ref: ArtifactReference{Kind: ArtifactTrace, Path: "runs/../trace.json", SHA256: valid.SHA256}},
		{name: "windows-drive", ref: ArtifactReference{Kind: ArtifactTrace, Path: "C:/trace.json", SHA256: valid.SHA256}},
		{name: "backslash", ref: ArtifactReference{Kind: ArtifactTrace, Path: `runs\\trace.json`, SHA256: valid.SHA256}},
		{name: "empty-sha", ref: ArtifactReference{Kind: ArtifactTrace, Path: valid.Path}},
		{name: "upper-sha", ref: ArtifactReference{Kind: ArtifactTrace, Path: valid.Path, SHA256: strings.ToUpper(valid.SHA256)}},
		{name: "short-sha", ref: ArtifactReference{Kind: ArtifactTrace, Path: valid.Path, SHA256: valid.SHA256[:63]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validBuildInput()
			input.Artifacts = []ArtifactReference{test.ref}
			if _, err := BuildV1(input); err == nil {
				t.Fatalf("reference unexpectedly accepted: %+v", test.ref)
			}
		})
	}

	input := validBuildInput()
	input.Artifacts = []ArtifactReference{valid, valid}
	if _, err := BuildV1(input); err == nil {
		t.Fatal("duplicate kind+path unexpectedly accepted")
	}
}

func TestBuildV1Replayability(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		record, err := BuildV1(validBuildInput())
		if err != nil || !record.Replay.Replayable {
			t.Fatalf("record/error = %+v/%v", record.Replay, err)
		}
		if record.Replay.TraceArtifact.SHA256 == record.Trace.Digest {
			t.Fatal("test setup accidentally equates file and semantic trace digests")
		}
	})
	tests := []struct {
		name   string
		mutate func(*BuildInput)
	}{
		{name: "missing-config", mutate: func(input *BuildInput) { removeArtifact(input, ArtifactConfig) }},
		{name: "missing-trace", mutate: func(input *BuildInput) { removeArtifact(input, ArtifactTrace) }},
		{name: "seed-mismatch", mutate: func(input *BuildInput) { input.Completion.Execution.Result.Trace.Seed++ }},
		{name: "invalid-identity", mutate: func(input *BuildInput) { input.Completion.Execution.Result.Trace.ExecutionID = "" }},
		{name: "no-valid-trace", mutate: func(input *BuildInput) {
			input.Completion.Run.TraceDigest = ""
			input.Completion.Execution.Result.Trace.Version = 0
			input.Completion.Execution.Result.Trace.ExecutionID = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validBuildInput()
			test.mutate(&input)
			record, err := BuildV1(input)
			if err != nil {
				t.Fatal(err)
			}
			if record.Replay.Replayable {
				t.Fatalf("record is unexpectedly replayable: %+v", record.Replay)
			}
		})
	}
}

func TestRecordV1DigestDeterministic(t *testing.T) {
	input := validBuildInput()
	var want string
	for iteration := 0; iteration < 20; iteration++ {
		record, err := BuildV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			want = record.RecordDigest
		} else if record.RecordDigest != want {
			t.Fatalf("iteration %d digest = %s, want %s", iteration, record.RecordDigest, want)
		}
	}
}

func TestRecordV1DigestIgnoresDebugAndArtifactReferences(t *testing.T) {
	base := validBuildInput()
	want, err := BuildV1(base)
	if err != nil {
		t.Fatal(err)
	}
	variants := []func(*BuildInput){
		func(input *BuildInput) { input.Completion.Execution.Result.Error = "different engine debug" },
		func(input *BuildInput) { input.Completion.Run.Error = "different run debug" },
		func(input *BuildInput) { input.Completion.Execution.Result.TerminationDetail = "different detail" },
		func(input *BuildInput) { input.Artifacts[0].Path = "elsewhere/result.json" },
		func(input *BuildInput) { input.Artifacts[0].SHA256 = digestOf('f') },
		func(input *BuildInput) {
			for left, right := 0, len(input.Artifacts)-1; left < right; left, right = left+1, right-1 {
				input.Artifacts[left], input.Artifacts[right] = input.Artifacts[right], input.Artifacts[left]
			}
		},
	}
	for index, mutate := range variants {
		input := validBuildInput()
		mutate(&input)
		got, err := BuildV1(input)
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		if got.RecordDigest != want.RecordDigest {
			t.Fatalf("variant %d digest = %s, want %s", index, got.RecordDigest, want.RecordDigest)
		}
	}
}

func TestRecordV1DigestChangesOnRecordIdentityOrOutcome(t *testing.T) {
	base, err := BuildV1(validBuildInput())
	if err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name   string
		mutate func(*BuildInput)
	}{
		{name: "candidate-id", mutate: func(input *BuildInput) {
			input.Completion.Candidate.ID = "candidate-other"
			input.Completion.Run.CandidateID = "candidate-other"
		}},
		{name: "run-index", mutate: func(input *BuildInput) { input.Completion.Run.Index++ }},
		{name: "seed", mutate: func(input *BuildInput) { input.Completion.Run.Seed++ }},
		{name: "plan-digest", mutate: func(input *BuildInput) { input.Completion.Run.PlanDigest = digestOf('d') }},
		{name: "trace-digest", mutate: func(input *BuildInput) { input.Completion.Run.TraceDigest = digestOf('d') }},
		{name: "engine-status", mutate: func(input *BuildInput) { input.Completion.Execution.Result.Status = engine.StatusRuntimeFailed }},
		{name: "experiment-status", mutate: func(input *BuildInput) { input.Completion.Run.Status = engine.StatusMappingFailed }},
		{name: "model-count", mutate: func(input *BuildInput) {
			input.Completion.Execution.Result.ModelEvents = append(input.Completion.Execution.Result.ModelEvents, model.NewEvent("Second", nil))
			input.Completion.Run.ModelEvents++
		}},
		{name: "oracle-code", mutate: func(input *BuildInput) {
			input.Completion.Execution.Result.OracleFindings[0].Code = "different"
		}},
		{name: "failure-signature", mutate: func(input *BuildInput) {
			input.Completion.Execution.Result.Status = engine.StatusRuntimeFailed
			input.FailureSignature = FailureSignatureInput{
				Availability: FailureSignatureAvailable,
				Signature:    &minimize.Signature{Status: engine.StatusRuntimeFailed, FailureCode: "x"},
			}
		}},
		{name: "config-fingerprint", mutate: func(input *BuildInput) { input.ConfigurationFingerprint = digestOf('e') }},
		{name: "corpus-outcome", mutate: func(input *BuildInput) {
			input.Completion.Run.Retained = false
			input.Completion.Run.CorpusID = ""
			input.Completion.Run.CorpusAdmission = "rejected_raw_threshold"
		}},
	}
	for _, test := range variants {
		t.Run(test.name, func(t *testing.T) {
			input := validBuildInput()
			test.mutate(&input)
			record, err := BuildV1(input)
			if err != nil {
				t.Fatal(err)
			}
			if record.RecordDigest == base.RecordDigest {
				t.Fatalf("digest did not change: %s", record.RecordDigest)
			}
		})
	}
}

func TestBuildV1DefensiveCopies(t *testing.T) {
	input := validBuildInput()
	signature := minimize.Signature{Status: engine.StatusOracleFailed, OracleCodes: []string{"z:two", "a:one"}}
	input.Completion.Execution.Result.Status = engine.StatusOracleFailed
	input.FailureSignature = FailureSignatureInput{Availability: FailureSignatureAvailable, Signature: &signature}
	record, err := BuildV1(input)
	if err != nil {
		t.Fatal(err)
	}
	wantArtifacts := append([]ArtifactReference(nil), record.Artifacts...)
	wantCodes := append([]string(nil), record.Oracle.Codes...)
	wantSignatureCodes := append([]string(nil), record.Failure.Signature.OracleCodes...)

	input.Artifacts[0].Path = "mutated"
	input.Completion.Execution.Result.OracleFindings[0].Code = "mutated"
	input.Completion.Execution.Plan.Actions[0].Kind = plan.ActionHeal
	signature.OracleCodes[0] = "mutated"

	if !reflect.DeepEqual(record.Artifacts, wantArtifacts) ||
		!reflect.DeepEqual(record.Oracle.Codes, wantCodes) ||
		!reflect.DeepEqual(record.Failure.Signature.OracleCodes, wantSignatureCodes) {
		t.Fatalf("record shares caller memory: %+v", record)
	}
}

func TestBuildV1DoesNotMutateCompletion(t *testing.T) {
	input := validBuildInput()
	signature := minimize.Signature{Status: engine.StatusOracleFailed, OracleCodes: []string{"z:two", "a:one", "z:two"}}
	input.Completion.Execution.Result.Status = engine.StatusOracleFailed
	input.FailureSignature = FailureSignatureInput{Availability: FailureSignatureAvailable, Signature: &signature}
	before := snapshotBuildInput(t, input)
	if _, err := BuildV1(input); err != nil {
		t.Fatal(err)
	}
	after := snapshotBuildInput(t, input)
	if before != after {
		t.Fatal("BuildV1 mutated Completion, artifacts, or caller signature")
	}
}

func TestBuildV1RejectsInconsistentCompletion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BuildInput)
	}{
		{name: "run-not-completed", mutate: func(input *BuildInput) { input.Completion.Run.Completed = false }},
		{name: "empty-candidate-id", mutate: func(input *BuildInput) {
			input.Completion.Candidate.ID = ""
			input.Completion.Run.CandidateID = ""
		}},
		{name: "candidate-id", mutate: func(input *BuildInput) { input.Completion.Run.CandidateID = "other" }},
		{name: "candidate-kind", mutate: func(input *BuildInput) { input.Completion.Run.CandidateKind = experiment.CandidateMutation }},
		{name: "candidate-parent", mutate: func(input *BuildInput) { input.Completion.Run.ParentID = "other" }},
		{name: "candidate-source", mutate: func(input *BuildInput) { input.Completion.Run.Source = "other" }},
		{name: "candidate-depth", mutate: func(input *BuildInput) { input.Completion.Run.Depth++ }},
		{name: "negative-run-index", mutate: func(input *BuildInput) { input.Completion.Run.Index = -1 }},
		{name: "configuration-fingerprint", mutate: func(input *BuildInput) { input.ConfigurationFingerprint = "bad" }},
		{name: "empty-plan", mutate: func(input *BuildInput) { input.Completion.Execution.Plan.Actions = nil }},
		{name: "plan-digest", mutate: func(input *BuildInput) { input.Completion.Run.PlanDigest = "bad" }},
		{name: "action-count", mutate: func(input *BuildInput) { input.Completion.Run.Actions++ }},
		{name: "effect-count", mutate: func(input *BuildInput) { input.Completion.Run.Effects++ }},
		{name: "model-event-count", mutate: func(input *BuildInput) { input.Completion.Run.ModelEvents++ }},
		{name: "model-state-count", mutate: func(input *BuildInput) { input.Completion.Run.ModelStates++ }},
		{name: "oracle-count", mutate: func(input *BuildInput) { input.Completion.Run.OracleFindings++ }},
		{name: "budget", mutate: func(input *BuildInput) { input.Completion.Run.BudgetExhausted = true }},
		{name: "termination", mutate: func(input *BuildInput) { input.Completion.Run.Termination = engine.TerminationPolicyComplete }},
		{name: "termination-code", mutate: func(input *BuildInput) { input.Completion.Run.TerminationCode = "other" }},
		{name: "trace-digest", mutate: func(input *BuildInput) { input.Completion.Run.TraceDigest = "" }},
		{name: "trace-version", mutate: func(input *BuildInput) { input.Completion.Execution.Result.Trace.Version = 99 }},
		{name: "state-path-digest", mutate: func(input *BuildInput) { input.Completion.Run.ModelStatePathDigest = "" }},
		{name: "digest-without-states", mutate: func(input *BuildInput) {
			input.Completion.Execution.Result.ModelStates = nil
			input.Completion.Run.ModelStates = 0
		}},
		{name: "retained-without-corpus", mutate: func(input *BuildInput) { input.Completion.Run.CorpusID = "" }},
		{name: "unretained-with-corpus", mutate: func(input *BuildInput) { input.Completion.Run.Retained = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validBuildInput()
			test.mutate(&input)
			if _, err := BuildV1(input); err == nil {
				t.Fatal("BuildV1 unexpectedly accepted inconsistent completion")
			}
		})
	}
}

func TestBuildV1ConsumesExistingExperimentDigests(t *testing.T) {
	runner, err := experiment.New(experiment.Config{
		Runs: 1, BaseSeed: 700, Parallelism: 1, InitialPopulation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var completion experiment.Completion
	options := experiment.FeedbackOptions{
		Mutator: inertMutator{},
		Hooks: experiment.Hooks{OnRunComplete: func(value experiment.Completion) error {
			completion = value
			return nil
		}},
	}
	execute := func(_ context.Context, _ int, seed int64, _ experiment.Candidate) (experiment.FeedbackExecution, error) {
		sequence := plan.PlanSequence{
			Actions:  []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}},
			Metadata: map[string]string{"source": "runner"},
		}
		return experiment.FeedbackExecution{
			Plan: sequence,
			Result: engine.Result{
				Status: engine.StatusCompleted,
				Trace: core.Trace{
					Version: core.CurrentTraceVersion, ExecutionID: "runner-digest", Seed: seed,
				},
				ModelStates: []model.State{{Key: 9}},
			},
		}, nil
	}
	if _, _, err := runner.RunFeedback(context.Background(), options, execute); err != nil {
		t.Fatal(err)
	}
	if completion.Run.PlanDigest == "" || completion.Run.TraceDigest == "" ||
		completion.Run.ModelStatePathDigest == "" {
		t.Fatalf("runner did not supply digests: %+v", completion.Run)
	}
	originalPlanDigest := completion.Run.PlanDigest
	completion.Execution.Plan.Actions[0] = plan.PlanAction{Kind: plan.ActionTimeout, Node: 2}
	input := BuildInput{
		Completion:               completion,
		ConfigurationFingerprint: digestOf('f'),
		FailureSignature:         FailureSignatureInput{Availability: FailureSignatureNotApplicable},
	}
	record, err := BuildV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if record.Plan.Digest != originalPlanDigest ||
		record.Trace.Digest != completion.Run.TraceDigest ||
		record.Model.StatePathDigest != completion.Run.ModelStatePathDigest {
		t.Fatalf("builder did not consume runner digests: %+v", record)
	}
}

func validBuildInput() BuildInput {
	result := engine.Result{
		Status:            engine.StatusCompleted,
		Error:             "engine debug text",
		ModelExecuted:     true,
		Termination:       engine.TerminationPlanComplete,
		TerminationDetail: "plan finished",
		Resolutions:       []plan.Resolution{{}},
		Actions:           core.ActionSequence{Actions: []core.Action{}},
		Trace: core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "execution-42", Seed: 42,
			Steps: []core.StepRecord{},
		},
		ModelEvents: []model.Event{model.NewEvent("Timeout", map[string]any{"node": 1})},
		ModelStates: []model.State{{Text: "s1", Key: 1}, {Text: "s2", Key: 2}},
		OracleFindings: []oracle.Finding{
			{Oracle: "raft-log", Code: "commit"},
			{Oracle: "raft-agreement", Code: "leader"},
			{Oracle: "raft-log", Code: "commit"},
		},
	}
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
	candidatePlan := sequence.Copy()
	completion := experiment.Completion{
		Candidate: experiment.Candidate{
			ID: "candidate-000001", Kind: experiment.CandidateInitial, Source: "random_init",
			Plan: &candidatePlan,
		},
		Run: experiment.Run{
			Completed: true, Index: 1, Seed: 42, Status: engine.StatusCompleted, Succeeded: true,
			Actions: 0, Effects: 0, ModelEvents: 1, ModelStates: 2, OracleFindings: 3,
			CandidateID: "candidate-000001", CandidateKind: experiment.CandidateInitial,
			Source: "random_init", Retained: true, CorpusID: "corpus-000001",
			CorpusAdmission: "retained_raw", Termination: engine.TerminationPlanComplete,
			PlanDigest: digestOf('a'), TraceDigest: digestOf('b'), ModelStatePathDigest: digestOf('c'),
		},
		Execution: experiment.FeedbackExecution{Plan: sequence, Result: result},
	}
	return BuildInput{
		Completion:               completion,
		ConfigurationFingerprint: digestOf('d'),
		Artifacts: []ArtifactReference{
			{Kind: ArtifactTrace, Path: "runs/000001/trace.json", SHA256: digestOf('1')},
			{Kind: ArtifactConfig, Path: "config.json", SHA256: digestOf('2')},
			{Kind: ArtifactPlan, Path: "runs/000001/plan.json", SHA256: digestOf('3')},
			{Kind: ArtifactModelEvents, Path: "runs/000001/model-events.json", SHA256: digestOf('4')},
			{Kind: ArtifactModelStates, Path: "runs/000001/model-states.json", SHA256: digestOf('5')},
			{Kind: ArtifactOracleFindings, Path: "runs/000001/oracle-findings.json", SHA256: digestOf('6')},
			{Kind: ArtifactResult, Path: "runs/000001/result.json", SHA256: digestOf('7')},
		},
		FailureSignature: FailureSignatureInput{Availability: FailureSignatureNotApplicable},
	}
}

func digestOf(character byte) string {
	return strings.Repeat(string(character), 64)
}

func removeArtifact(input *BuildInput, kind ArtifactKind) {
	filtered := input.Artifacts[:0]
	for _, artifact := range input.Artifacts {
		if artifact.Kind != kind {
			filtered = append(filtered, artifact)
		}
	}
	input.Artifacts = filtered
}

func snapshotBuildInput(t *testing.T, input BuildInput) string {
	t.Helper()
	value := struct {
		Run       experiment.Run
		Candidate experiment.Candidate
		Plan      plan.PlanSequence
		Result    engine.Result
		Artifacts []ArtifactReference
		Failure   FailureSignatureInput
	}{
		Run: input.Completion.Run, Candidate: input.Completion.Candidate,
		Plan: input.Completion.Execution.Plan, Result: input.Completion.Execution.Result,
		Artifacts: input.Artifacts, Failure: input.FailureSignature,
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type inertMutator struct{}

func (inertMutator) Name() string { return "inert" }
func (inertMutator) Mutate(context.Context, mutation.Request) ([]plan.PlanSequence, error) {
	return nil, fmt.Errorf("inert mutator must not run")
}
