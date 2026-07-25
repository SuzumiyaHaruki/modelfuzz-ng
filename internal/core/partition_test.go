package core

import "testing"

func TestNetworkPartitionValidationCoverageAndLinks(t *testing.T) {
	partition := NetworkPartition{Groups: [][]NodeID{{1, 2}, {3}, {4, 5}}}
	if err := partition.Validate(); err != nil {
		t.Fatal(err)
	}
	if !partition.Covers([]NodeID{1, 2, 3, 4, 5}) || partition.Covers([]NodeID{1, 2, 3}) {
		t.Fatal("partition coverage mismatch")
	}
	if partition.Blocks(LinkID{From: 1, To: 2}) || !partition.Blocks(LinkID{From: 2, To: 3}) {
		t.Fatal("partition link classification mismatch")
	}
	for _, invalid := range []NetworkPartition{
		{},
		{Groups: [][]NodeID{{1, 2}}},
		{Groups: [][]NodeID{{1}, {}}},
		{Groups: [][]NodeID{{1}, {1, 2}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid partition accepted: %+v", invalid)
		}
	}
}
