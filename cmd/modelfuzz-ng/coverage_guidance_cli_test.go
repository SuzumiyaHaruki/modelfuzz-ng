package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func TestCoverageGuidanceCLIRejectsUnknownAndLegacyEnergy(t *testing.T) {
	for _, arguments := range [][]string{
		{"experiment", "-output", filepath.Join(t.TempDir(), "unknown"), "-coverage-guidance-mode", "unknown"},
		{"experiment", "-output", filepath.Join(t.TempDir(), "energy"), "-coverage-guidance-mode", "facet-fixed"},
	} {
		err := runCLI(context.Background(), arguments, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("arguments unexpectedly accepted: %v", arguments)
		}
	}
}

func TestCoverageBenchmarkManifestPreservesExplicitFalseAndRejectsUnknownField(t *testing.T) {
	data := []byte(`{
		"schema":"facet-guidance-benchmark-v1","name":"test","phase":"pilot",
		"config":"config.json","modes":["facet-fixed"],"seeds":[1],
		"runs":1,"max_plan_actions":1,"initial_population":1,"parallelism":1,
		"fixed_energy":1,"fixed_parent_selection":"admission-fifo-once",
		"coverage_corpus_limit":1,"max_ready_candidates":1,"artifact_policy":"summary",
		"record_all_coverage_metrics":false,"offline_goal_evaluation":false
	}`)
	manifest, err := decodeCoverageBenchmarkManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RecordAllCoverageMetrics || manifest.OfflineGoalEvaluation {
		t.Fatalf("explicit false was overwritten: %+v", manifest)
	}
	if _, err := decodeCoverageBenchmarkManifest(
		append(data[:len(data)-1], []byte(`,"unknown":true}`)...)); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
}

func TestCoverageGuidanceCLIProducesRecomputableArtifacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"largest_term": 5, "max_log_index": 5,
			})
		case "/metrics":
			_ = json.NewEncoder(writer).Encode(map[string]any{})
		case "/execute":
			var events []model.Event
			if err := json.NewDecoder(request.Body).Decode(&events); err != nil {
				t.Error(err)
				return
			}
			states := make([]string, len(events))
			keys := make([]int64, len(events))
			for index := range states {
				states[index] = validCoverageTLCState("follower")
				keys[index] = int64(index + 1)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"States": states, "Keys": keys})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "facet-guided")
	var stderr bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", output, "-runs", "3", "-initial-population", "1",
		"-max-plan-actions", "6", "-artifact-policy", "all", "-seed", "1701",
		"-tlc", server.URL, "-coverage-guidance-mode", "facet-fixed",
		"-coverage-energy-mode", "fixed", "-fixed-energy", "2",
		"-coverage-corpus-limit", "10",
	}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("guided experiment: %v\nstderr=%s", err, stderr.String())
	}
	for _, name := range []string{
		"coverage-guidance-settings.json", "coverage-observations.jsonl",
		"corpus-decisions.jsonl", "parent-selection.jsonl",
		"coverage-guidance-summary.json", "cross-coverage-summary.json",
		"facet-growth.csv", "interaction-growth.csv", "corpus-efficiency.csv",
		"offline-goal-evaluation.json",
	} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	observations, err := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
		filepath.Join(output, "coverage-observations.jsonl"), 3)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := persistence.ReadJSONLines[coverageguidance.Decision](
		filepath.Join(output, "corpus-decisions.jsonl"), 3)
	if err != nil {
		t.Fatal(err)
	}
	recomputed, _, err := coverageguidance.Recompute(coverageguidance.Config{
		Mode: coverageguidance.ModeFacetFixed, FixedEnergy: 2, CorpusLimit: 10,
	}, observations)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recomputed, decisions) {
		t.Fatal("persisted decisions differ from offline recomputation")
	}
	var summary coverageguidance.Summary
	if err := persistence.ReadJSON(filepath.Join(output, "coverage-guidance-summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Candidates != 3 || summary.Mode != coverageguidance.ModeFacetFixed ||
		summary.FinalCoverage.Raw == 0 || summary.FinalCoverage.Facets["election"] == 0 {
		t.Fatalf("summary = %+v", summary)
	}
	data, err := os.ReadFile(filepath.Join(output, "experiment-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"coverage_guidance_mode": "facet-fixed"`) {
		t.Fatalf("settings do not expose guidance mode: %s", data)
	}
}
