package core

// FailureKind 区分被测系统 panic 和普通 Adapter/Runtime 执行错误。
type FailureKind string

const (
	FailureSUTPanic     FailureKind = "sut_panic"
	FailureRuntimeError FailureKind = "runtime_error"
)

func (k FailureKind) Valid() bool {
	return k == FailureSUTPanic || k == FailureRuntimeError
}

// FailureRecord 描述一条没有形成合法 After 状态的失败操作。失败 Action 不写入
// Trace.Steps，因为它不是可严格重放的完整状态转换；这里单独保存执行前全局状态、
// panic 值和 stack，供定位及后续失败重放使用。
type FailureRecord struct {
	Kind              FailureKind `json:"kind"`
	Operation         string      `json:"operation"`
	Time              LogicalTime `json:"time"`
	Action            *Action     `json:"action,omitempty"`
	Error             string      `json:"error"`
	PanicValue        string      `json:"panic_value,omitempty"`
	Stack             string      `json:"stack,omitempty"`
	ObservationBefore Observation `json:"observation_before"`
}

func (f FailureRecord) Validate() error {
	if !f.Kind.Valid() {
		return invalidValue("failure_record", "kind", "is unknown")
	}
	if f.Operation == "" {
		return invalidValue("failure_record", "operation", "must not be empty")
	}
	if f.Error == "" {
		return invalidValue("failure_record", "error", "must not be empty")
	}
	if f.Kind == FailureSUTPanic && (f.PanicValue == "" || f.Stack == "") {
		return invalidValue("failure_record", "panic", "panic value and stack are required")
	}
	if f.Action != nil {
		if err := f.Action.Validate(); err != nil {
			return invalidValue("failure_record", "action", err.Error())
		}
	}
	if err := f.ObservationBefore.Validate(); err != nil {
		return invalidValue("failure_record", "observation_before", err.Error())
	}
	return nil
}

func (f FailureRecord) Copy() FailureRecord {
	copy := f
	if f.Action != nil {
		action := f.Action.Copy()
		copy.Action = &action
	}
	copy.ObservationBefore = f.ObservationBefore.Copy()
	return copy
}
