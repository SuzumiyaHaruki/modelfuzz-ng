package main

import (
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
