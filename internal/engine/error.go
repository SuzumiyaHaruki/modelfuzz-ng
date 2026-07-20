package engine

import "errors"

var (
	ErrInvalidConfig = errors.New("invalid engine config")
	ErrInvalidPlan   = errors.New("invalid plan sequence")
	ErrResolution    = errors.New("plan resolution failed")
	ErrRuntime       = errors.New("runtime execution failed")
	ErrMapping       = errors.New("model mapping failed")
	ErrModel         = errors.New("model execution failed")
)
