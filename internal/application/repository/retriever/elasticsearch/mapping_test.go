package elasticsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFolderIDQueryField(t *testing.T) {
	tests := []struct {
		name      string
		property  any
		wantField string
		wantError string
	}{
		{
			name:      "keyword",
			property:  map[string]any{"type": "keyword"},
			wantField: "folder_id",
		},
		{
			name: "text with keyword multi-field",
			property: map[string]any{
				"type": "text",
				"fields": map[string]any{
					"keyword": map[string]any{"type": "keyword"},
				},
			},
			wantField: "folder_id.keyword",
		},
		{
			name:      "text without keyword multi-field",
			property:  map[string]any{"type": "text"},
			wantError: "has no keyword multi-field",
		},
		{
			name: "text with incompatible keyword multi-field",
			property: map[string]any{
				"type": "text",
				"fields": map[string]any{
					"keyword": map[string]any{"type": "text"},
				},
			},
			wantError: "incompatible type",
		},
		{
			name:      "unknown type",
			property:  map[string]any{"type": "long"},
			wantError: "incompatible type",
		},
		{
			name:      "missing type",
			property:  map[string]any{},
			wantError: "incompatible type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, err := FolderIDQueryField(tt.property)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Empty(t, field)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantField, field)
		})
	}
}
