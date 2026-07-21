package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
)

func TestArtifactPolicies(t *testing.T) {
	success := experiment.Run{Succeeded: true}
	retained := experiment.Run{Succeeded: true, Retained: true}
	failure := experiment.Run{Succeeded: false}
	tests := []struct {
		policy                     artifactPolicy
		success, retained, failure bool
	}{
		{artifactsAll, true, true, true},
		{artifactsRetained, false, true, true},
		{artifactsFailures, false, false, true},
		{artifactsSummary, false, false, false},
	}
	for _, test := range tests {
		if test.policy.saves(success) != test.success || test.policy.saves(retained) != test.retained ||
			test.policy.saves(failure) != test.failure {
			t.Fatalf("policy %s does not match expected decisions", test.policy)
		}
	}
}

func TestOpenExperimentStoreTrimsUncommittedRunSummaries(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "runs.jsonl")
	if err := os.WriteFile(path, []byte("{\"index\":0}\n{\"index\":1}\n{\"index\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := openExperimentStore(directory, artifactsSummary, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 2 {
		t.Fatalf("runs.jsonl retained %d lines: %q", lines, data)
	}
}

func TestOpenExperimentStoreTrimsAndLoadsCommittedCorpusEntries(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "corpus.jsonl")
	data := "{\"id\":\"corpus-000000\"}\n{\"id\":\"corpus-000001\"}\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := openExperimentStore(directory, artifactsSummary, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.corpusEntries) != 1 || store.corpusEntries[0].ID != "corpus-000000" {
		t.Fatalf("loaded corpus entries = %+v", store.corpusEntries)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(retained), "\n") != 1 {
		t.Fatalf("corpus.jsonl was not trimmed: %q", retained)
	}
}
