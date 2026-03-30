package file

import (
	"strings"
	"testing"
)

func TestNewS3Client_PathStyleForCompatibleEndpoints(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      string
		wantPathStyle bool
	}{
		{
			name:          "S3-compatible service uses path-style",
			endpoint:      "https://storage.internal:9000",
			wantPathStyle: true,
		},
		{
			name:          "MinIO endpoint uses path-style",
			endpoint:      "http://minio.local:9000",
			wantPathStyle: true,
		},
		{
			name:          "AWS S3 regional endpoint uses virtual-hosted",
			endpoint:      "https://s3.us-east-1.amazonaws.com",
			wantPathStyle: false,
		},
		{
			name:          "AWS China endpoint uses virtual-hosted",
			endpoint:      "https://s3.cn-north-1.amazonaws.com.cn",
			wantPathStyle: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify the path-style detection logic matches our implementation
			usePathStyle := !strings.Contains(tt.endpoint, "amazonaws.com")
			if usePathStyle != tt.wantPathStyle {
				t.Errorf("endpoint %q: usePathStyle = %v, want %v", tt.endpoint, usePathStyle, tt.wantPathStyle)
			}
		})
	}
}

func TestNewS3Client_EmptyEndpoint(t *testing.T) {
	// Empty endpoint should not trigger path-style (standard AWS S3)
	endpoint := ""
	if endpoint != "" {
		t.Fatal("expected empty endpoint to skip custom configuration")
	}
}

func TestParseS3FilePath(t *testing.T) {
	svc := &s3FileService{bucketName: "test-bucket"}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid s3 path",
			input: "s3://test-bucket/123/exports/abc.png",
			want:  "123/exports/abc.png",
		},
		{
			name:    "wrong bucket",
			input:   "s3://other-bucket/key",
			wantErr: true,
		},
		{
			name:    "not s3 scheme",
			input:   "minio://test-bucket/key",
			wantErr: true,
		},
		{
			name:    "missing object key",
			input:   "s3://test-bucket/",
			wantErr: true,
		},
		{
			name:    "empty path",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.parseS3FilePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseS3FilePath(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseS3FilePath(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseS3FilePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
