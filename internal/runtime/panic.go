package runtime

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// SUTPanicError 把 Adapter/SUT 边界外逃的 panic 转成普通错误，同时保留原始
// panic 值和捕获点的完整 goroutine stack。
type SUTPanicError struct {
	Operation string
	Value     string
	Stack     string
}

func (e *SUTPanicError) Error() string {
	return fmt.Sprintf("%s panicked: %s", e.Operation, e.Value)
}

func (e *SUTPanicError) Unwrap() error {
	return ErrSUTPanic
}

func callSUT[T any](operation string, call func() (T, error)) (result T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &SUTPanicError{
				Operation: operation,
				Value:     fmt.Sprint(recovered),
				Stack:     string(debug.Stack()),
			}
		}
	}()
	return call()
}

func (r *Runtime) recordFailure(operation string, action *core.Action, before core.Observation, err error) {
	if r.failure != nil || err == nil {
		return
	}
	record := core.FailureRecord{
		Kind: core.FailureRuntimeError, Operation: operation, Time: r.time,
		Error: err.Error(), ObservationBefore: before.Copy(),
	}
	if action != nil {
		copy := action.Copy()
		record.Action = &copy
	}
	var panicError *SUTPanicError
	if errors.As(err, &panicError) {
		record.Kind = core.FailureSUTPanic
		record.Operation = panicError.Operation
		record.PanicValue = panicError.Value
		record.Stack = panicError.Stack
	}
	r.failure = &record
}
