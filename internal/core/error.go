package core

import (
	"errors"
	"fmt"
)

// ErrInvalidValue 是 core 中所有数据校验错误共同包装的哨兵错误。
var ErrInvalidValue = errors.New("invalid core value")

// ValidationError 用于说明 core 数据中的哪个字段不合法。
type ValidationError struct {
	Type   string
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%s: %s", e.Type, e.Reason)
	}
	return fmt.Sprintf("%s.%s: %s", e.Type, e.Field, e.Reason)
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidValue
}

func invalidValue(typ, field, reason string) error {
	return &ValidationError{Type: typ, Field: field, Reason: reason}
}
