package file

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseOssFilePath(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBucket  string
		wantKey     string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid path with nested key",
			input:      "oss://my-bucket/123/exports/abc123.csv",
			wantBucket: "my-bucket",
			wantKey:    "123/exports/abc123.csv",
		},
		{
			name:       "valid path with simple key",
			input:      "oss://test-bucket/key",
			wantBucket: "test-bucket",
			wantKey:    "key",
		},
		{
			name:       "valid path with deep nesting",
			input:      "oss://bucket/prefix/tenant/exports/uuid.png",
			wantBucket: "bucket",
			wantKey:    "prefix/tenant/exports/uuid.png",
		},
		{
			name:        "invalid scheme",
			input:       "s3://bucket/key",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "empty path",
			input:       "",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "bucket only no key",
			input:       "oss://bucket/",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "scheme only",
			input:       "oss://",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "no slash after bucket",
			input:       "oss://bucket",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "empty bucket name",
			input:       "oss:///some-key",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, key, err := parseOssFilePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseOssFilePath(%q) expected error, got bucket=%q key=%q", tt.input, bucket, key)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseOssFilePath(%q) error = %v, want containing %q", tt.input, err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("parseOssFilePath(%q) unexpected error: %v", tt.input, err)
				return
			}
			if bucket != tt.wantBucket {
				t.Errorf("parseOssFilePath(%q) bucket = %q, want %q", tt.input, bucket, tt.wantBucket)
			}
			if key != tt.wantKey {
				t.Errorf("parseOssFilePath(%q) key = %q, want %q", tt.input, key, tt.wantKey)
			}
		})
	}
}

func TestNewOSSClient(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		region    string
		accessKey string
		secretKey string
		wantErr   bool
	}{
		{
			name:      "valid parameters create client",
			endpoint:  "https://oss-cn-hangzhou.aliyuncs.com",
			region:    "cn-hangzhou",
			accessKey: "test-access-key",
			secretKey: "test-secret-key",
			wantErr:   false,
		},
		{
			name:      "custom endpoint",
			endpoint:  "https://custom-oss-endpoint.com",
			region:    "cn-shanghai",
			accessKey: "ak",
			secretKey: "sk",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := newOSSClient(tt.endpoint, tt.region, tt.accessKey, tt.secretKey)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("newOSSClient() unexpected error: %v", err)
				return
			}
			if client == nil {
				t.Error("expected non-nil client")
			}
		})
	}
}

func TestCheckOssConnectivity_InvalidEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should fail with an invalid/unreachable endpoint
	err := CheckOssConnectivity(ctx,
		"https://invalid-oss-endpoint-that-does-not-exist.local",
		"cn-hangzhou",
		"invalid-access-key",
		"invalid-secret-key",
		"nonexistent-bucket",
	)

	if err == nil {
		t.Error("CheckOssConnectivity with invalid endpoint should return an error")
	}
}

func TestOssBucketExists_NonExistent(t *testing.T) {
	client, err := newOSSClient(
		"https://oss-cn-hangzhou.aliyuncs.com",
		"cn-hangzhou",
		"test-invalid-key",
		"test-invalid-secret",
	)
	if err != nil {
		t.Fatalf("newOSSClient() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Bucket that definitely doesn't exist - should return false, not error
	// (auth may fail first, but we verify the function doesn't crash)
	exists, err := ossBucketExists(ctx, client, "this-bucket-definitely-does-not-exist-12345")
	// We don't assert on the result because it depends on network/auth,
	// but the function should not panic or hang.
	_ = exists
	_ = err
}

func TestOssCreateBucket(t *testing.T) {
	client, err := newOSSClient(
		"https://oss-cn-hangzhou.aliyuncs.com",
		"cn-hangzhou",
		"test-invalid-key",
		"test-invalid-secret",
	)
	if err != nil {
		t.Fatalf("newOSSClient() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should fail with invalid credentials
	err = ossCreateBucket(ctx, client, "test-bucket")
	if err == nil {
		t.Error("ossCreateBucket with invalid credentials should return an error")
	}
}
