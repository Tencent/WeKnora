package types

import (
	"fmt"
	"strings"
	"testing"
)

func TestMCPRequestMetaConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *MCPRequestMetaConfig
		wantErr bool
	}{
		{name: "nil", config: nil},
		{
			name: "valid allowlist",
			config: &MCPRequestMetaConfig{
				Headers:    []string{"Authorization", "X-Trace-Id"},
				BodyFields: []string{"channel", "knowledge_base_ids", "mcp_metadata.user_role"},
			},
		},
		{
			name:    "reject header injection",
			config:  &MCPRequestMetaConfig{Headers: []string{"X-Trace\r\nX-Evil"}},
			wantErr: true,
		},
		{
			name:    "reject binary body field",
			config:  &MCPRequestMetaConfig{BodyFields: []string{"attachment_uploads"}},
			wantErr: true,
		},
		{
			name:    "reject malformed custom metadata selector",
			config:  &MCPRequestMetaConfig{BodyFields: []string{"mcp_metadata.user role"}},
			wantErr: true,
		},
		{
			name: "reject too many headers",
			config: &MCPRequestMetaConfig{
				Headers: make([]string, MCPRequestMetaMaxSelectors+1),
			},
			wantErr: true,
		},
		{
			name: "reject oversized metadata key",
			config: &MCPRequestMetaConfig{
				BodyFields: []string{"mcp_metadata." + strings.Repeat("x", 129)},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMCPRequestMetaEmpty(t *testing.T) {
	var nilMeta *MCPRequestMeta
	if !nilMeta.Empty() {
		t.Fatal("nil metadata should be empty")
	}
	if !(&MCPRequestMeta{}).Empty() {
		t.Fatal("zero metadata should be empty")
	}
	if (&MCPRequestMeta{Headers: map[string]string{"X-Trace-Id": "abc"}}).Empty() {
		t.Fatal("metadata with a header should not be empty")
	}
}

func TestValidateMCPRequestMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		wantErr  bool
	}{
		{
			name:     "valid scalar values",
			metadata: map[string]string{"user_role": "auditor", "trace.id": "trace-1"},
		},
		{
			name: "reject too many values",
			metadata: func() map[string]string {
				values := make(map[string]string, MCPRequestMetaMaxSelectors+1)
				for i := 0; i <= MCPRequestMetaMaxSelectors; i++ {
					values[fmt.Sprintf("key_%d", i)] = "value"
				}
				return values
			}(),
			wantErr: true,
		},
		{
			name:     "reject invalid key",
			metadata: map[string]string{"user role": "auditor"},
			wantErr:  true,
		},
		{
			name:     "reject oversized value",
			metadata: map[string]string{"token": strings.Repeat("x", MCPRequestMetaMaxValueBytes+1)},
			wantErr:  true,
		},
		{
			name: "reject oversized aggregate",
			metadata: map[string]string{
				"one":   strings.Repeat("x", 7000),
				"two":   strings.Repeat("x", 7000),
				"three": strings.Repeat("x", 7000),
				"four":  strings.Repeat("x", 7000),
				"five":  strings.Repeat("x", 7000),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMCPRequestMetadata(tt.metadata)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateMCPRequestMetadata() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
