package types

import "testing"

func TestParseProviderScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"local://tenant/file.pdf", "local"},
		{"minio://bucket/key", "minio"},
		{"cos://bucket/key", "cos"},
		{"tos://bucket/key", "tos"},
		{"s3://bucket/key", "s3"},
		{"s3://my-bucket/weknora/123/exports/abc.png", "s3"},
		{"https://example.com/img.png", ""},
		{"http://localhost:9000/bucket/key", ""},
		{"/data/files/images/abc.png", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseProviderScheme(tt.input)
			if got != tt.want {
				t.Errorf("ParseProviderScheme(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInferStorageFromFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"local://tenant/file.pdf", "local"},
		{"minio://bucket/key", "minio"},
		{"cos://bucket/key", "cos"},
		{"tos://bucket/key", "tos"},
		{"s3://bucket/key", "s3"},
		{"https://my-bucket.cos.ap-guangzhou.myqcloud.com/key", "cos"},
		{"https://example.com/img.png", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := InferStorageFromFilePath(tt.input)
			if got != tt.want {
				t.Errorf("InferStorageFromFilePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
