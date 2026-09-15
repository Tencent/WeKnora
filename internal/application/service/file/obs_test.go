package file

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestObsS3OptionsUseVirtualHostedStyleForDomainEndpoints(t *testing.T) {
	const endpoint = "https://obs.cn-south-1.myhuaweicloud.com"
	opts := obsS3Options(endpoint, "cn-south-1", "ak", "sk")

	if opts.UsePathStyle {
		t.Fatal("domain endpoint must use virtual-hosted addressing; Huawei Cloud rejects path-style since 2023-12-30 (issue #3269)")
	}
	if opts.BaseEndpoint == nil || *opts.BaseEndpoint != endpoint {
		t.Fatalf("BaseEndpoint = %v, want %q", opts.BaseEndpoint, endpoint)
	}
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired {
		t.Fatalf("RequestChecksumCalculation = %v, want WhenRequired (aligned with the S3 driver)", opts.RequestChecksumCalculation)
	}
	if opts.EndpointResolver != nil {
		t.Fatal("the deprecated custom resolver must stay dropped; its HostnameImmutable=true forced path-style addressing")
	}
	if opts.HTTPClient == nil {
		t.Fatal("SSRF-safe HTTP client must be configured")
	}
}

func TestObsS3OptionsKeepPathStyleForIPLiteralEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"http://192.168.1.10:9000",
		"https://[2001:db8::1]:443",
		"not a url",
	} {
		if opts := obsS3Options(endpoint, "region", "ak", "sk"); !opts.UsePathStyle {
			t.Fatalf("endpoint %q: expected defensive path-style fallback", endpoint)
		}
	}
}

func TestObsGetFileURLVirtualHostedStyle(t *testing.T) {
	s := &obsFileService{
		bucketName: "my-bucket",
		endpoint:   "https://obs.cn-south-1.myhuaweicloud.com",
	}

	got, err := s.GetFileURL(context.Background(), "obs://my-bucket/42/kb/file.pdf")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://my-bucket.obs.cn-south-1.myhuaweicloud.com/42/kb/file.pdf"
	if got != want {
		t.Fatalf("GetFileURL = %q, want %q", got, want)
	}
}

func TestObsGetFileURLPreservesEndpointPort(t *testing.T) {
	s := &obsFileService{
		bucketName: "my-bucket",
		endpoint:   "https://obs.example.com:8443",
	}

	got, err := s.GetFileURL(context.Background(), "obs://my-bucket/key.bin")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://my-bucket.obs.example.com:8443/key.bin"
	if got != want {
		t.Fatalf("GetFileURL = %q, want %q", got, want)
	}
}

func TestObsGetFileURLKeepsPathStyleForIPEndpoints(t *testing.T) {
	s := &obsFileService{
		bucketName: "my-bucket",
		endpoint:   "http://192.168.1.10:9000",
	}

	got, err := s.GetFileURL(context.Background(), "obs://my-bucket/key.bin")
	if err != nil {
		t.Fatal(err)
	}
	want := "http://192.168.1.10:9000/my-bucket/key.bin"
	if got != want {
		t.Fatalf("GetFileURL = %q, want %q", got, want)
	}
}

func TestObsGetFileURLProxyDomainAndHTTPPassthrough(t *testing.T) {
	proxy := &obsFileService{
		bucketName:  "my-bucket",
		proxyDomain: "https://cdn.example.com",
	}
	got, err := proxy.GetFileURL(context.Background(), "https://cdn.example.com/42/kb/file.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.com/42/kb/file.pdf" {
		t.Fatalf("proxy GetFileURL = %q", got)
	}

	s := &obsFileService{
		bucketName: "my-bucket",
		endpoint:   "https://obs.cn-south-1.myhuaweicloud.com",
	}
	got, err = s.GetFileURL(context.Background(), "https://elsewhere.example.com/file")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://elsewhere.example.com/file" {
		t.Fatalf("http(s) passthrough = %q", got)
	}
}
