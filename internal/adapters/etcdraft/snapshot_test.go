package etcdraft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

func TestSnapshotPrefixConflictIsRejected(t *testing.T) {
	config := testConfig(1)
	confState := &raftpb.ConfState{Voters: []uint64{1}}
	n, err := newNode(config, core.NodeID(1), confState, newNodeRand(42, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.recordCommitted(&raftpb.Entry{Index: proto.Uint64(1), Term: proto.Uint64(1), Data: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	data, err := n.snapshotData(1)
	if err != nil {
		t.Fatal(err)
	}
	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	conflict := sha256.Sum256([]byte("conflicting committed prefix"))
	state.PrefixDigests[1] = hex.EncodeToString(conflict[:])
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &raftpb.Snapshot{Data: data, Metadata: &raftpb.SnapshotMetadata{
		Index: proto.Uint64(1), Term: proto.Uint64(1), ConfState: confState,
	}}
	if err := n.restoreSnapshotPrefix(snapshot); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("snapshot conflict error = %v", err)
	}
}

func TestSnapshotDataRejectsCorruption(t *testing.T) {
	snapshot := &raftpb.Snapshot{Data: []byte("not-json"), Metadata: &raftpb.SnapshotMetadata{Index: proto.Uint64(1)}}
	if _, err := decodeSnapshotState(snapshot); err == nil {
		t.Fatal("corrupted snapshot data was accepted")
	}
}
