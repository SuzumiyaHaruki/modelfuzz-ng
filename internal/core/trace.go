package core

const CurrentTraceVersion uint32 = 2

// StepRecord 表示具体 Trace 中一次已经解析并执行的状态转换。
type StepRecord struct {
	Index      uint64      `json:"index"`
	TimeBefore LogicalTime `json:"time_before"`
	TimeAfter  LogicalTime `json:"time_after"`
	Action     Action      `json:"action"`
	Effects    []Effect    `json:"effects,omitempty"`
	// NodesBefore/NodesAfter 保存协议节点的轻量状态快照，使模型事件能够在
	// 不重新执行 SUT 的情况下由持久化 Trace 重建。网络消息仍由 Action 和
	// Effect 表达，不在每一步重复保存完整 Observation。
	NodesBefore       []NodeObservation `json:"nodes_before,omitempty"`
	NodesAfter        []NodeObservation `json:"nodes_after,omitempty"`
	ObservationDigest string            `json:"observation_digest,omitempty"`
}

func (s StepRecord) Validate() error {
	if s.TimeAfter < s.TimeBefore {
		return invalidValue("step_record", "time_after", "must not precede time_before")
	}
	if err := s.Action.Validate(); err != nil {
		return invalidValue("step_record", "action", err.Error())
	}
	if s.Action.Kind == ActionAdvanceTime {
		if s.TimeAfter <= s.TimeBefore {
			return invalidValue("step_record", "time_after", "advance-time action must move time forward")
		}
		if s.Action.TargetTime != s.TimeAfter {
			return invalidValue("step_record", "time_after", "must equal advance-time target")
		}
	} else if s.TimeAfter != s.TimeBefore {
		return invalidValue("step_record", "time_after", "only advance-time actions may move logical time")
	}
	for i, effect := range s.Effects {
		if effect.At < s.TimeBefore || effect.At > s.TimeAfter {
			return invalidValue("step_record", "effects", "effect time is outside the step interval")
		}
		if i > 0 && effect.At < s.Effects[i-1].At {
			return invalidValue("step_record", "effects", "effect times must be non-decreasing")
		}
		if err := effect.Validate(); err != nil {
			return invalidValue("step_record", "effects", err.Error())
		}
	}
	if err := validateNodeSnapshot(s.NodesBefore); err != nil {
		return invalidValue("step_record", "nodes_before", err.Error())
	}
	if err := validateNodeSnapshot(s.NodesAfter); err != nil {
		return invalidValue("step_record", "nodes_after", err.Error())
	}
	return nil
}

func validateNodeSnapshot(nodes []NodeObservation) error {
	return (Observation{Nodes: nodes}).Validate()
}

func (s StepRecord) Copy() StepRecord {
	copy := s
	copy.Action = s.Action.Copy()
	copy.Effects = make([]Effect, len(s.Effects))
	for i, effect := range s.Effects {
		copy.Effects[i] = effect.Copy()
	}
	copy.NodesBefore = make([]NodeObservation, len(s.NodesBefore))
	for i, node := range s.NodesBefore {
		copy.NodesBefore[i] = node.Copy()
	}
	copy.NodesAfter = make([]NodeObservation, len(s.NodesAfter))
	for i, node := range s.NodesAfter {
		copy.NodesAfter[i] = node.Copy()
	}
	return copy
}

// Trace 是可序列化的具体执行轨迹。Mutation 和 replay 算法位于 core 之外，
// 以该数据结构作为输入。
type Trace struct {
	Version     uint32            `json:"version"`
	ExecutionID ExecutionID       `json:"execution_id"`
	Seed        int64             `json:"seed"`
	Steps       []StepRecord      `json:"steps"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func NewTrace(id ExecutionID, seed int64) *Trace {
	return &Trace{
		Version:     CurrentTraceVersion,
		ExecutionID: id,
		Seed:        seed,
		Steps:       make([]StepRecord, 0),
	}
}

func (t *Trace) Append(step StepRecord) error {
	if t == nil {
		return invalidValue("trace", "", "is nil")
	}
	if err := step.Validate(); err != nil {
		return err
	}
	if t.Version >= 2 && (len(step.NodesBefore) == 0 || len(step.NodesAfter) == 0) {
		return invalidValue("step_record", "nodes", "version 2 requires before/after node snapshots")
	}
	if expected := uint64(len(t.Steps)); step.Index != expected {
		return invalidValue("step_record", "index", "must be the next contiguous index")
	}
	if len(t.Steps) > 0 && step.TimeBefore != t.Steps[len(t.Steps)-1].TimeAfter {
		return invalidValue("step_record", "time_before", "must equal the previous step's time_after")
	}
	t.Steps = append(t.Steps, step.Copy())
	return nil
}

func (t Trace) Validate() error {
	if t.Version == 0 || t.Version > CurrentTraceVersion {
		return invalidValue("trace", "version", "is unsupported")
	}
	if !t.ExecutionID.Valid() {
		return invalidValue("trace", "execution_id", "must not be empty")
	}
	for i, step := range t.Steps {
		if step.Index != uint64(i) {
			return invalidValue("trace", "steps", "indices must be contiguous and zero-based")
		}
		if err := step.Validate(); err != nil {
			return err
		}
		if t.Version >= 2 && (len(step.NodesBefore) == 0 || len(step.NodesAfter) == 0) {
			return invalidValue("trace", "steps", "version 2 steps require before/after node snapshots")
		}
		if i > 0 && step.TimeBefore != t.Steps[i-1].TimeAfter {
			return invalidValue("trace", "steps", "logical time must be contiguous")
		}
	}
	return nil
}

func (t Trace) Copy() Trace {
	copy := t
	copy.Steps = make([]StepRecord, len(t.Steps))
	for i, step := range t.Steps {
		copy.Steps[i] = step.Copy()
	}
	copy.Metadata = cloneStringMap(t.Metadata)
	return copy
}
