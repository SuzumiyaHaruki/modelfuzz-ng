package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func Load(path string) (core.Trace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Trace{}, fmt.Errorf("read trace %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var trace core.Trace
	if err := decoder.Decode(&trace); err != nil {
		return core.Trace{}, fmt.Errorf("decode trace %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return core.Trace{}, fmt.Errorf("decode trace %s: trailing JSON content", path)
	}
	if err := trace.Validate(); err != nil {
		return core.Trace{}, fmt.Errorf("%w: %v", ErrInvalidTrace, err)
	}
	return trace, nil
}
