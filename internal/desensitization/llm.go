package desensitization

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

func maskWithLLM(_ context.Context, _ string, _ types.DesensitizationConfig, _ Deps) (string, types.JSONMap, error) {
	return "", nil, fmt.Errorf("%w: llm", ErrEngineUnsupported)
}
