package raft

import "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"

func CatalogV1() []facet.Evaluator {
	return []facet.Evaluator{
		NewElectionRoleTermShapeV1(),
		NewReplicationAlignmentShapeV1(),
		NewSnapshotLifecycleEventV1(),
	}
}
