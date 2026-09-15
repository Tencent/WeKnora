package file

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestNewObsClient_UsesVirtualHostedStyle(t *testing.T) {
	const endpoint = "https://obs.cn-north-4.myhuaweicloud.com"
	t.Setenv("SSRF_WHITELIST", "obs.cn-north-4.myhuaweicloud.com")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	svc, err := newObsClient(endpoint, "cn-north-4", "ak", "sk", "test-bucket", "")
	if err != nil {
		t.Fatalf("newObsClient() error = %v", err)
	}

	opts := svc.client.Options()
	if opts.UsePathStyle {
		t.Fatal("UsePathStyle = true, want false (Huawei OBS requires virtual-hosted style)")
	}
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired {
		t.Errorf("RequestChecksumCalculation = %v, want WhenRequired", opts.RequestChecksumCalculation)
	}

	resolved, err := opts.EndpointResolver.ResolveEndpoint("cn-north-4", s3.EndpointResolverOptions{})
	if err != nil {
		t.Fatalf("ResolveEndpoint() error = %v", err)
	}
	if !resolved.HostnameImmutable {
		t.Fatal("HostnameImmutable = false, want true so the SDK does not rewrite Huawei's host")
	}
	if resolved.URL != endpoint {
		t.Errorf("resolver URL = %q, want %q", resolved.URL, endpoint)
	}
}

func TestVirtualHostedOBSURL(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		bucket    string
		objectKey string
		want      string
		wantErr   bool
	}{
		{
			name:      "regional Huawei endpoint",
			endpoint:  "https://obs.cn-north-4.myhuaweicloud.com",
			bucket:    "test-bucket",
			objectKey: "docs/a.pdf",
			want:      "https://test-bucket.obs.cn-north-4.myhuaweicloud.com/docs/a.pdf",
		},
		{
			name:      "trailing slash on endpoint",
			endpoint:  "https://obs.cn-east-3.myhuaweicloud.com/",
			bucket:    "kb",
			objectKey: "/prefix/key.txt",
			want:      "https://kb.obs.cn-east-3.myhuaweicloud.com/prefix/key.txt",
		},
		{
			name:     "missing host",
			endpoint: "https://",
			bucket:   "kb",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := virtualHostedOBSURL(tt.endpoint, tt.bucket, tt.objectKey)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("virtualHostedOBSURL() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("virtualHostedOBSURL() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("virtualHostedOBSURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestObsGetFileURL_VirtualHosted(t *testing.T) {
	svc := &obsFileService{
		endpoint:   "https://obs.cn-north-4.myhuaweicloud.com",
		bucketName: "test-bucket",
	}

	got, err := svc.GetFileURL(context.Background(), "obs://test-bucket/docs/a.pdf")
	if err != nil {
		t.Fatalf("GetFileURL() error = %v", err)
	}
	want := "https://test-bucket.obs.cn-north-4.myhuaweicloud.com/docs/a.pdf"
	if got != want {
		t.Errorf("GetFileURL() = %q, want %q", got, want)
	}
}

func TestObsGetFileURL_HTTPPassthrough(t *testing.T) {
	svc := &obsFileService{
		endpoint:   "https://obs.cn-north-4.myhuaweicloud.com",
		bucketName: "test-bucket",
	}

	in := "https://cdn.example.com/docs/a.pdf"
	got, err := svc.GetFileURL(context.Background(), in)
	if err != nil {
		t.Fatalf("GetFileURL() error = %v", err)
	}
	if got != in {
		t.Errorf("GetFileURL() = %q, want %q", got, in)
	}
}
