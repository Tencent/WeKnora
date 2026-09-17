package tools

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func mcpServiceWithTimeout(seconds int) *types.MCPService {
	if seconds <= 0 {
		return &types.MCPService{}
	}
	return &types.MCPService{AdvancedConfig: &types.MCPAdvancedConfig{Timeout: seconds}}
}

func TestServiceCallTimeout(t *testing.T) {
	tests := []struct {
		name           string
		serviceTimeout int
		expected       time.Duration
	}{
		{"nil advanced config", 0, 0},
		{"zero timeout", 0, 0},
		{"negative timeout", -5, 0},
		{"positive timeout", 300, 300 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &MCPTool{service: mcpServiceWithTimeout(tt.serviceTimeout)}
			assert.Equal(t, tt.expected, tool.serviceCallTimeout())
		})
	}

	t.Run("nil service", func(t *testing.T) {
		tool := &MCPTool{}
		assert.Equal(t, time.Duration(0), tool.serviceCallTimeout())
	})
}

// TestCallToolTimeout covers the per-call budget derivation for MCP CallTool
// (#3135): the service's advanced_config.timeout extends the engine-derived
// window when longer, and never shortens it.
func TestCallToolTimeout(t *testing.T) {
	const engineWindow = 60 * time.Second

	tests := []struct {
		name           string
		serviceTimeout int
		engineTimeout  time.Duration
		expected       time.Duration
	}{
		{
			name:           "no advanced config keeps engine window",
			serviceTimeout: 0,
			engineTimeout:  engineWindow,
			expected:       engineWindow,
		},
		{
			name:           "longer service timeout wins (#3135)",
			serviceTimeout: 300,
			engineTimeout:  engineWindow,
			expected:       300 * time.Second,
		},
		{
			name:           "equal service timeout keeps engine window",
			serviceTimeout: 60,
			engineTimeout:  engineWindow,
			expected:       engineWindow,
		},
		{
			name:           "shorter service timeout never shortens",
			serviceTimeout: 30,
			engineTimeout:  engineWindow,
			expected:       engineWindow,
		},
		{
			name:           "missing engine timeout falls back to 60s then extends",
			serviceTimeout: 300,
			engineTimeout:  0,
			expected:       300 * time.Second,
		},
		{
			name:           "missing engine timeout with short service keeps 60s",
			serviceTimeout: 30,
			engineTimeout:  0,
			expected:       engineWindow,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &MCPTool{service: mcpServiceWithTimeout(tt.serviceTimeout)}
			assert.Equal(t, tt.expected, tool.callToolTimeout(tt.engineTimeout))
		})
	}
}
