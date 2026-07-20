// Package model 定义 Concrete Trace 到形式化模型事件之间的通用边界。
package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidEvent      = errors.New("invalid model event")
	ErrInvalidTransition = errors.New("invalid model transition")
)

// Event 是发送给模型执行器的一条协议语义事件。
// Reset 事件没有 Name 和 Params；普通事件必须有 Name 且 Reset 为 false。
type Event struct {
	Name   string         `json:"name,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Reset  bool           `json:"reset,omitempty"`
}

func NewEvent(name string, params map[string]any) Event {
	return Event{Name: name, Params: cloneMap(params)}
}

func ResetEvent() Event {
	return Event{Reset: true}
}

func (e Event) Validate() error {
	if e.Reset {
		if e.Name != "" || len(e.Params) != 0 {
			return fmt.Errorf("%w: reset event must not contain name or params", ErrInvalidEvent)
		}
		return nil
	}
	if e.Name == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidEvent)
	}
	if _, err := json.Marshal(e.Params); err != nil {
		return fmt.Errorf("%w: params are not JSON serializable: %v", ErrInvalidEvent, err)
	}
	return nil
}

func (e Event) Copy() Event {
	return Event{Name: e.Name, Params: cloneMap(e.Params), Reset: e.Reset}
}

// cloneMap 是一个深拷贝 map[string]any 的函数，确保在复制事件时不会共享底层数据结构。
func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneValue(value)
	}
	return result
}

// cloneValue 是一个深拷贝任意类型的函数，支持 map[string]any、[]any、[]map[string]any 和 []byte 类型。
func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneMap(value)
	case []any:
		result := make([]any, len(value))
		for i := range value {
			result[i] = cloneValue(value[i])
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(value))
		for i := range value {
			result[i] = cloneMap(value[i])
		}
		return result
	case []byte:
		return append([]byte(nil), value...)
	default:
		return value
	}
}
