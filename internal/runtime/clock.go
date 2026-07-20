package runtime

import (
	"context"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// advanceTime 将跨越式时间推进拆成逐单位 Tick，每轮立即处理 Adapter Effect。
func (r *Runtime) advanceTime(ctx context.Context, target core.LogicalTime) ([]core.Effect, error) {
	if target <= r.time {
		return nil, fmt.Errorf(
			"%w: target time %d must be greater than current time %d",
			ErrInvalidAction, target, r.time,
		)
	}

	effects := make([]core.Effect, 0)
	for r.time < target {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.time++
		current, err := r.adapter.Tick(ctx, r.time)
		if err != nil {
			return nil, fmt.Errorf("%w: tick at %d: %v", ErrAdapter, r.time, err)
		}
		concrete, err := r.processAdapterEffects(current, r.time)
		if err != nil {
			return nil, err
		}
		if err := validateNaturalTimeoutEffects(concrete); err != nil {
			return nil, err
		}
		effects = append(effects, concrete...)
	}
	return effects, nil
}
