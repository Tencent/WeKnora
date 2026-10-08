package file

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// capturingRoundTripper records the first HTTP request URL the S3 client
// would send, without dialing the network. That is the invariant #3269
// actually cares about: PutObject must hit {bucket}.{host}/{key}, not
// {host}/{bucket}/{key}. Checking UsePathStyle alone is not enough — PR
// #3272 set that flag while HostnameImmutable still forced path-style.
type capturingRoundTripper struct {
	host string
	path string
}

func (c *capturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if c.host == "" {
		c.host = req.URL.Host
		c.path = req.URL.Path
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Etag": []string{`"test"`}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}, nil
}

func putObjectWithCapture(t *testing.T, endpoint, region, bucket, key string) (host, path string) {
	t.Helper()
	trip := &capturingRoundTripper{}
	opts := obsS3Options(endpoint, region, "ak", "sk")
	opts.HTTPClient = &http.Client{Transport: trip}
	opts.Retryer = aws.NopRetryer{}
	client := s3.New(opts)
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader("hello"),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if trip.host == "" {
		t.Fatal("PutObject did not issue an HTTP request")
	}
	return trip.host, trip.path
}

func TestObsS3OptionsUseVirtualHostedStyleForDomainEndpoints(t *testing.T) {
	const endpoint = "https://obs.cn-south-1.myhuaweicloud.com"
	opts := obsS3Options(endpoint, "cn-south-1", "ak", "sk")

	if opts.UsePathStyle {
		t.Fatal("domain endpoint must use virtual-hosted addressing (issue #3269)")
	}
	if opts.BaseEndpoint == nil || *opts.BaseEndpoint != endpoint {
		t.Fatalf("BaseEndpoint = %v, want %q", opts.BaseEndpoint, endpoint)
	}
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired {
		t.Fatalf("RequestChecksumCalculation = %v, want WhenRequired", opts.RequestChecksumCalculation)
	}
	//nolint:staticcheck // v1 resolver must stay unset; HostnameImmutable pinned path-style
	if opts.EndpointResolver != nil {
		t.Fatal("deprecated custom resolver must stay dropped")
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

func TestObsPutObjectUsesVirtualHostedURL(t *testing.T) {
	host, path := putObjectWithCapture(t,
		"https://obs.cn-south-1.myhuaweicloud.com",
		"cn-south-1",
		"my-bucket",
		"42/kb/file.pdf",
	)
	wantHost := "my-bucket.obs.cn-south-1.myhuaweicloud.com"
	if host != wantHost {
		t.Fatalf("PutObject host = %q, want virtual-hosted %q", host, wantHost)
	}
	if path != "/42/kb/file.pdf" {
		t.Fatalf("PutObject path = %q, want /42/kb/file.pdf", path)
	}
}

func TestObsPutObjectKeepsPathStyleForIPLiteralEndpoints(t *testing.T) {
	host, path := putObjectWithCapture(t,
		"http://192.168.1.10:9000",
		"region",
		"my-bucket",
		"key.bin",
	)
	if host != "192.168.1.10:9000" {
		t.Fatalf("PutObject host = %q, want 192.168.1.10:9000", host)
	}
	if path != "/my-bucket/key.bin" {
		t.Fatalf("PutObject path = %q, want /my-bucket/key.bin", path)
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
