package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckStorageEngineProviderConfig(t *testing.T) {
	for _, provider := range []string{"oss", "ks3", "s3"} {
		t.Run(provider, func(t *testing.T) {
			var request StorageCheckRequest
			input := `{"provider":"` + provider + `","` + provider + `":{"bucket_name":"example-bucket"}}`
			if err := json.Unmarshal([]byte(input), &request); err != nil {
				t.Fatal(err)
			}
			transport := tagTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.Path != "/api/v1/system/storage-engine-check" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				var received map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
					t.Fatal(err)
				}
				var config map[string]string
				if err := json.Unmarshal(received[provider], &config); err != nil {
					t.Errorf("provider config lost: %v", err)
				}
				if config["bucket_name"] != "example-bucket" {
					t.Errorf("config = %v, want the supplied bucket", config)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{"ok":true,"message":"connected"}}`)),
					Header:     make(http.Header),
				}, nil
			})
			result, err := NewClient("https://example.test", WithTransport(transport)).CheckStorageEngine(
				context.Background(), &request,
			)
			if err != nil || result == nil || !result.OK {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
		})
	}
}
