package engine

import "errors"

var (
	ErrInvalidConfig = errors.New("invalid engine config")
	ErrInvalidPlan   = errors.New("invalid plan sequence")
	ErrResolution    = errors.New("plan resolution failed")
	ErrRuntime       = errors.New("runtime execution failed")
	ErrMapping       = errors.New("model mapping failed")
	ErrUnsupported   = errors.New("action unsupported by model")
	ErrModel         = errors.New("model execution failed")
)
