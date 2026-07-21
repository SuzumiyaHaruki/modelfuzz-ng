package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
)

type raftSettings struct {
	NodeIDs          []core.NodeID `json:"node_ids"`
	ElectionTick     int           `json:"election_tick"`
	HeartbeatTick    int           `json:"heartbeat_tick"`
	MaxSizePerMsg    uint64        `json:"max_size_per_message"`
	MaxInflightMsgs  int           `json:"max_inflight_messages"`
	MaxInflightBytes uint64        `json:"max_inflight_bytes"`
	VerboseLogging   bool          `json:"verbose_logging"`
}

func (s raftSettings) adapterConfig() etcdraft.Config {
	return etcdraft.Config{
		NodeIDs:          append([]core.NodeID(nil), s.NodeIDs...),
		ElectionTick:     s.ElectionTick,
		HeartbeatTick:    s.HeartbeatTick,
		MaxSizePerMsg:    s.MaxSizePerMsg,
		MaxInflightMsgs:  s.MaxInflightMsgs,
		MaxInflightBytes: s.MaxInflightBytes,
	}
}

type tlcSettings struct {
	Address        string `json:"address,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type cliConfig struct {
	ExecutionID core.ExecutionID    `json:"execution_id"`
	Seed        int64               `json:"seed"`
	Raft        raftSettings        `json:"raft"`
	Model       raftmodel.Config    `json:"model"`
	Runtime     runtimepkg.Limits   `json:"runtime_limits"`
	Resolver    plan.ResolverConfig `json:"resolver"`
	Engine      engine.Config       `json:"engine"`
	TLC         tlcSettings         `json:"tlc"`
}

func defaultCLIConfig() cliConfig {
	raftDefaults := etcdraft.DefaultConfig()
	return cliConfig{
		ExecutionID: "modelfuzz-ng",
		Seed:        1,
		Raft: raftSettings{
			NodeIDs:          append([]core.NodeID(nil), raftDefaults.NodeIDs...),
			ElectionTick:     raftDefaults.ElectionTick,
			HeartbeatTick:    raftDefaults.HeartbeatTick,
			MaxSizePerMsg:    raftDefaults.MaxSizePerMsg,
			MaxInflightMsgs:  raftDefaults.MaxInflightMsgs,
			MaxInflightBytes: raftDefaults.MaxInflightBytes,
		},
		Model: raftmodel.DefaultConfig(),
		Runtime: runtimepkg.Limits{
			MaxActions: 10000, MaxTicks: 10000, MaxEffects: 100000, MaxQueuedMessages: 100000,
		},
		Resolver: plan.DefaultResolverConfig(),
		Engine: engine.Config{
			MaxPlanActions: 256, MaxConsecutiveNoops: 32,
		},
		TLC: tlcSettings{TimeoutSeconds: 30},
	}
}

func loadCLIConfig(path string) (cliConfig, error) {
	config := defaultCLIConfig()
	if path == "" {
		return config, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cliConfig{}, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	if err := decodeStrictJSON(data, &config); err != nil {
		return cliConfig{}, fmt.Errorf("解析配置 %s: %w", path, err)
	}

	// model.node_ids 未显式提供时继承 Raft 节点，既减少重复配置，也避免只改
	// 一处后形成看似正常、实际无法映射的实验。
	var presence struct {
		Model struct {
			NodeIDs json.RawMessage `json:"node_ids"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return cliConfig{}, fmt.Errorf("检查模型节点配置: %w", err)
	}
	if len(presence.Model.NodeIDs) == 0 || bytes.Equal(bytes.TrimSpace(presence.Model.NodeIDs), []byte("null")) {
		config.Model.NodeIDs = append([]core.NodeID(nil), config.Raft.NodeIDs...)
	}
	return config, nil
}

func readPlan(path string) (plan.PlanSequence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return plan.PlanSequence{}, fmt.Errorf("读取 Plan %s: %w", path, err)
	}
	var sequence plan.PlanSequence
	if err := decodeStrictJSON(data, &sequence); err != nil {
		return plan.PlanSequence{}, fmt.Errorf("解析 Plan %s: %w", path, err)
	}
	return sequence, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON 只能包含一个顶层值")
		}
		return fmt.Errorf("JSON 尾部存在无效内容: %w", err)
	}
	return nil
}

func validateAlignedNodes(raftNodes, modelNodes []core.NodeID) error {
	if len(raftNodes) != len(modelNodes) {
		return fmt.Errorf("raft 有 %d 个节点，但模型有 %d 个节点", len(raftNodes), len(modelNodes))
	}
	modelSet := make(map[core.NodeID]struct{}, len(modelNodes))
	for _, id := range modelNodes {
		if _, exists := modelSet[id]; exists {
			return fmt.Errorf("模型节点 %s 重复", id)
		}
		modelSet[id] = struct{}{}
	}
	for _, id := range raftNodes {
		if _, exists := modelSet[id]; !exists {
			return fmt.Errorf("raft 节点 %s 不在模型 Server 中", id)
		}
	}
	return nil
}
