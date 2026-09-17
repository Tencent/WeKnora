package service

import (
	"errors"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
)

func TestValidateJSONUploadContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		content  string
		wantErr  string
	}{
		{
			name:     "non-json skipped",
			filename: "notes.md",
			content:  "not json at all",
		},
		{
			name:     "valid object",
			filename: "config.json",
			content:  `{"agents":{"list":[]}}`,
		},
		{
			name:     "valid array",
			filename: "items.JSON",
			content:  `[1,2,3]`,
		},
		{
			name:     "jsonc comments rejected",
			filename: "openclaw.json",
			content:  "{\n// comment\n\"a\": 1\n}",
			wantErr:  "invalid JSON content",
		},
		{
			name:     "leading junk rejected",
			filename: "openclaw.json",
			content:  "openclaw.json\n\n{\"a\":1}",
			wantErr:  "invalid JSON content",
		},
		{
			name:     "trailing comma rejected",
			filename: "bad.json",
			content:  `{"a":1,}`,
			wantErr:  "invalid JSON content",
		},
		{
			name:     "empty rejected",
			filename: "empty.json",
			content:  "",
			wantErr:  "empty JSON content",
		},
		{
			name:     "bom-prefixed valid",
			filename: "bom.json",
			content:  "\ufeff{\"ok\":true}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			file := newMultipartFileHeader(t, tt.filename, tt.content)
			err := ValidateJSONUploadContent(tt.filename, file)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateJSONUploadContent() unexpected err: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateJSONUploadContent() err = nil, want %q", tt.wantErr)
			}
			appErr, ok := werrors.IsAppError(err)
			if !ok {
				t.Fatalf("want AppError, got %T (%v)", err, err)
			}
			if appErr.HTTPCode != 400 {
				t.Fatalf("HTTPCode = %d, want 400", appErr.HTTPCode)
			}
			if !errors.Is(err, appErr) || appErr.Message != tt.wantErr {
				if appErr.Message != tt.wantErr {
					t.Fatalf("Message = %q, want %q", appErr.Message, tt.wantErr)
				}
			}
		})
	}
}
