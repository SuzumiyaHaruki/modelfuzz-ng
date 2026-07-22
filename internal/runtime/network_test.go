package runtime

import (
	"errors"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func outbound(from, to core.NodeID, kind string) core.Message {
	return core.Message{
		From:        from,
		To:          to,
		SenderEpoch: 1,
		TypeHint:    kind,
		Payload:     kind,
	}
}

func TestNetworkAssignsStableIdentityAndPositions(t *testing.T) {
	network := newNetwork()
	first, err := network.registerOutbound(outbound(1, 2, "first"), 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := network.registerOutbound(outbound(1, 2, "second"), 4)
	if err != nil {
		t.Fatal(err)
	}
	other, err := network.registerOutbound(outbound(2, 1, "other"), 4)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != 1 || second.ID != 2 || other.ID != 3 {
		t.Fatalf("message IDs = %s, %s, %s; want m1, m2, m3", first.ID, second.ID, other.ID)
	}
	if first.Sequence != 1 || second.Sequence != 2 || other.Sequence != 1 {
		t.Fatalf("link sequences = %d, %d, %d; want 1, 2, 1", first.Sequence, second.Sequence, other.Sequence)
	}

	observations := network.observations()
	if len(observations) != 3 {
		t.Fatalf("observation count = %d, want 3", len(observations))
	}
	if observations[0].ID != first.ID || observations[0].Position != 0 || observations[0].EnqueuedAt != 3 {
		t.Fatalf("first observation = %+v", observations[0])
	}
	if observations[1].ID != second.ID || observations[1].Position != 1 || observations[1].EnqueuedAt != 4 {
		t.Fatalf("second observation = %+v", observations[1])
	}
}

func TestNetworkDuplicateAndRemoveUpdateCurrentPositions(t *testing.T) {
	network := newNetwork()
	first, err := network.registerOutbound(outbound(1, 2, "first"), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := network.registerOutbound(outbound(1, 2, "second"), 1)
	if err != nil {
		t.Fatal(err)
	}
	link := core.LinkID{From: 1, To: 2}

	duplicate, err := network.duplicate(first.ID, core.MessageSelector{Link: link, Position: 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != 3 || duplicate.Sequence != 3 || duplicate.ParentID != first.ID {
		t.Fatalf("duplicate = %+v", duplicate)
	}

	if _, err := network.remove(first.ID, core.MessageSelector{Link: link, Position: 0}); err != nil {
		t.Fatal(err)
	}
	observations := network.observations()
	if len(observations) != 2 || observations[0].ID != second.ID || observations[0].Position != 0 ||
		observations[1].ID != duplicate.ID || observations[1].Position != 1 {
		t.Fatalf("positions after remove = %+v", observations)
	}

	_, err = network.resolve(second.ID, core.MessageSelector{Link: link, Position: 1})
	if !errors.Is(err, ErrMessageUnavailable) {
		t.Fatalf("stale selector error = %v, want ErrMessageUnavailable", err)
	}
}

func TestNetworkPartitionMarksOnlyCrossGroupMessagesAndHealPreservesQueue(t *testing.T) {
	network := newNetwork()
	cross, err := network.registerOutbound(outbound(1, 2, "cross"), 1)
	if err != nil {
		t.Fatal(err)
	}
	within, err := network.registerOutbound(outbound(2, 3, "within"), 1)
	if err != nil {
		t.Fatal(err)
	}
	partition := core.NetworkPartition{Groups: [][]core.NodeID{{1}, {2, 3}}}
	if err := network.activatePartition(partition, []core.NodeID{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	observations := network.observations()
	if len(observations) != 2 || observations[0].ID != cross.ID || !observations[0].Blocked ||
		observations[1].ID != within.ID || observations[1].Blocked {
		t.Fatalf("partitioned observations = %+v", observations)
	}
	if err := network.heal(); err != nil {
		t.Fatal(err)
	}
	observations = network.observations()
	if len(observations) != 2 || observations[0].Blocked || observations[1].Blocked {
		t.Fatalf("healed observations = %+v", observations)
	}
}
