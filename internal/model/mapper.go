package model

import "fmt"

// Mapper 将一条实际执行转换映射为零到多条形式化模型事件。
// 零条是正常结果，例如丢包只改变 Runtime 网络，不改变当前 Raft 模型状态。
type Mapper interface {
	Map(Transition) ([]Event, error)
}

// MapAll 按 Concrete Trace 的顺序映射转换，并保持每一步内部的事件顺序。
func MapAll(mapper Mapper, transitions []Transition) ([]Event, error) {
	if mapper == nil {
		return nil, fmt.Errorf("mapper must not be nil")
	}
	result := make([]Event, 0)
	for i, transition := range transitions {
		events, err := mapper.Map(transition)
		if err != nil {
			return nil, fmt.Errorf("map transition %d: %w", i, err)
		}
		for j, event := range events {
			if err := event.Validate(); err != nil {
				return nil, fmt.Errorf("map transition %d event %d: %w", i, j, err)
			}
			result = append(result, event.Copy())
		}
	}
	return result, nil
}
