package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raft "go.etcd.io/raft/v3"
)

func TestReplayMatchesJSONRoundTrippedTrace(t *testing.T) {
	expected := produceElectionTrace(t)
	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var persisted core.Trace
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	replayer, err := NewReplayer(newRuntime(t, expected.ExecutionID, expected.Seed))
	if err != nil {
		t.Fatal(err)
	}
	result, err := replayer.Replay(context.Background(), persisted)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted || result.MatchedSteps != uint64(len(expected.Steps)) {
		t.Fatalf("replay result = %+v", result)
	}
	if equal, err := equalJSON(expected, result.Actual); err != nil || !equal {
		t.Fatalf("actual trace differs: equal=%v err=%v", equal, err)
	}
}

func TestReplayRejectsTamperedMessageIdentityBeforeExecution(t *testing.T) {
	expected := produceElectionTrace(t)
	expected.Steps[1].Action.Message++
	replayer, _ := NewReplayer(newRuntime(t, expected.ExecutionID, expected.Seed))
	result, err := replayer.Replay(context.Background(), expected)
	if !errors.Is(err, ErrDivergence) || result.Divergence == nil || result.Divergence.Field != "action" {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if result.MatchedSteps != 1 || len(result.Actual.Steps) != 1 {
		t.Fatalf("tampered action executed unexpectedly: %+v", result)
	}
}

func TestReplayFindsTamperedEffectAndDigest(t *testing.T) {
	tests := []struct {
		name  string
		field string
		edit  func(*core.Trace)
	}{
		{name: "effect", field: "effects", edit: func(trace *core.Trace) {
			trace.Steps[0].Effects[0].TimerFired.TypeHint = "changed"
		}},
		{name: "digest", field: "observation_digest", edit: func(trace *core.Trace) {
			trace.Steps[0].ObservationDigest = "changed"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := produceElectionTrace(t)
			test.edit(&expected)
			replayer, _ := NewReplayer(newRuntime(t, expected.ExecutionID, expected.Seed))
			result, err := replayer.Replay(context.Background(), expected)
			if !errors.Is(err, ErrDivergence) || result.Divergence == nil || result.Divergence.Field != test.field {
				t.Fatalf("result/error = %+v/%v", result, err)
			}
		})
	}
}

func TestReplayLegacyVersionsSkipLegacyObservationDigest(t *testing.T) {
	for _, version := range []uint32{2, 3} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			expected := produceElectionTrace(t)
			expected.Version = version
			for index := range expected.Steps {
				expected.Steps[index].ObservationDigest = "legacy-digest"
				expected.Steps[index].NodesBefore = compatibleNodes(expected.Steps[index].NodesBefore, version)
				expected.Steps[index].NodesAfter = compatibleNodes(expected.Steps[index].NodesAfter, version)
			}
			replayer, _ := NewReplayer(newRuntime(t, expected.ExecutionID, expected.Seed))
			result, err := replayer.Replay(context.Background(), expected)
			if err != nil || result.Status != StatusCompleted {
				t.Fatalf("v%d replay result/error = %+v/%v", version, result, err)
			}
		})
	}
}

func TestLoadRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.json")
	if err := writeTestFile(path, []byte(`{} {}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func produceElectionTrace(t *testing.T) core.Trace {
	t.Helper()
	runtime := newRuntime(t, "replay-test", 42)
	ctx := context.Background()
	observation, err := runtime.Reset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	step, err := runtime.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	observation = step.Observation
	vote := findObserved(t, observation, "MsgVote", 1, 2)
	step, err = runtime.Execute(ctx, deliver(vote))
	if err != nil {
		t.Fatal(err)
	}
	response := findObserved(t, step.Observation, "MsgVoteResp", 2, 1)
	leader, err := runtime.Execute(ctx, deliver(response))
	if err != nil {
		t.Fatal(err)
	}
	remainingVote := findObserved(t, leader.Observation, "MsgVote", 1, 3)
	drop := deliver(remainingVote)
	drop.Kind = core.ActionDrop
	if _, err := runtime.Execute(ctx, drop); err != nil {
		t.Fatal(err)
	}
	trace, err := runtime.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return trace
}

func newRuntime(t *testing.T, executionID core.ExecutionID, seed int64) *runtimepkg.Runtime {
	t.Helper()
	config := etcdraft.DefaultConfig()
	config.ElectionTick = 100
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: executionID, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func findObserved(t *testing.T, observation core.Observation, kind string, from, to core.NodeID) core.MessageObservation {
	t.Helper()
	for _, message := range observation.Messages {
		if message.TypeHint == kind && message.From == from && message.To == to {
			return message
		}
	}
	t.Fatalf("message %s %s->%s not found", kind, from, to)
	return core.MessageObservation{}
}

func deliver(message core.MessageObservation) core.Action {
	return core.Action{Kind: core.ActionDeliver, Message: message.ID, Selector: &core.MessageSelector{
		Link: core.LinkID{From: message.From, To: message.To}, Position: message.Position,
	}}
}

func writeTestFile(path string, data []byte) error {
	// 测试只写入 t.TempDir；使用标准文件 API 验证 Load 的真实文件边界。
	return os.WriteFile(path, data, 0o644)
}
