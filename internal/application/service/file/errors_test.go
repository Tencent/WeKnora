package file

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"

	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/smithy-go"
	ks3awserr "github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	cos "github.com/tencentyun/cos-go-sdk-v5"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

func TestIsFileNotFoundRecognizesWrappedProviderErrors(t *testing.T) {
	cosResponse := func(status int, code string) error {
		req := &http.Request{
			Method: http.MethodGet,
			URL:    &url.URL{Scheme: "https", Host: "cos.example.com", Path: "/bucket/key"},
		}
		return &cos.ErrorResponse{
			Response: &http.Response{StatusCode: status, Header: make(http.Header), Request: req},
			Code:     code,
		}
	}

	tests := map[string]error{
		"local":   &os.PathError{Op: "open", Path: "/missing", Err: os.ErrNotExist},
		"s3":      &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"},
		"s3 head": &smithy.GenericAPIError{Code: "NotFound", Message: "missing"},
		"ks3":     ks3awserr.New("NoSuchKey", "missing", nil),
		"minio":   minio.ErrorResponse{Code: "NoSuchKey", StatusCode: http.StatusNotFound},
		"cos":     cosResponse(http.StatusNotFound, "NoSuchKey"),
		"oss":     &oss.ServiceError{Code: "NoSuchKey", StatusCode: http.StatusNotFound},
		"tos":     &tos.TosServerError{Code: "NoSuchKey"},
	}

	for name, providerErr := range tests {
		t.Run(name, func(t *testing.T) {
			require.True(t, IsFileNotFound(fmt.Errorf("provider read failed: %w", providerErr)))
		})
	}
}

func TestIsFileNotFoundRejectsBucketPermissionAndTransportErrors(t *testing.T) {
	cosResponse := func(status int, code string) error {
		req := &http.Request{
			Method: http.MethodGet,
			URL:    &url.URL{Scheme: "https", Host: "cos.example.com", Path: "/bucket/key"},
		}
		return &cos.ErrorResponse{
			Response: &http.Response{StatusCode: status, Header: make(http.Header), Request: req},
			Code:     code,
		}
	}

	tests := map[string]error{
		"nil":                  nil,
		"s3 missing bucket":    &smithy.GenericAPIError{Code: "NoSuchBucket", Message: "missing bucket"},
		"s3 forbidden":         &smithy.GenericAPIError{Code: "AccessDenied", Message: "forbidden"},
		"ks3 missing bucket":   ks3awserr.New("NoSuchBucket", "missing bucket", nil),
		"minio missing bucket": minio.ErrorResponse{Code: "NoSuchBucket", StatusCode: http.StatusNotFound},
		"minio forbidden":      minio.ErrorResponse{Code: "AccessDenied", StatusCode: http.StatusForbidden},
		"cos missing bucket":   cosResponse(http.StatusNotFound, "NoSuchBucket"),
		"cos forbidden":        cosResponse(http.StatusForbidden, "AccessDenied"),
		"oss missing bucket":   &oss.ServiceError{Code: "NoSuchBucket", StatusCode: http.StatusNotFound},
		"tos missing bucket":   &tos.TosServerError{Code: "NoSuchBucket"},
		"canceled":             context.Canceled,
		"deadline":             context.DeadlineExceeded,
		"network timeout":      &url.Error{Op: "Get", URL: "https://storage.example.com/key", Err: timeoutError{}},
	}

	for name, providerErr := range tests {
		t.Run(name, func(t *testing.T) {
			require.False(t, IsFileNotFound(providerErr))
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}
