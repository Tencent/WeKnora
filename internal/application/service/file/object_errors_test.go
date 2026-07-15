package file

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	ks3awserr "github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

func TestNormalizeObjectReadErrorRecognizesProviderAbsence(t *testing.T) {
	tests := map[string]error{
		"minio": minio.ErrorResponse{Code: "NoSuchKey", StatusCode: http.StatusNotFound},
		"minio pointer": &minio.ErrorResponse{
			Code: "NoSuchKey", StatusCode: http.StatusNotFound,
		},
		"s3":  &types.NoSuchKey{},
		"obs": &types.NoSuchKey{},
		"cos": &cos.ErrorResponse{
			Code:     "NoSuchKey",
			Response: &http.Response{StatusCode: http.StatusNotFound},
		},
		"tos": &tos.TosServerError{
			RequestInfo: tos.RequestInfo{StatusCode: http.StatusNotFound},
			Code:        "NoSuchKey",
		},
		"oss": &aliyunoss.ServiceError{Code: "NoSuchKey", StatusCode: http.StatusNotFound},
		"ks3": ks3awserr.NewRequestFailure(
			ks3awserr.New("NoSuchKey", "missing", nil),
			http.StatusNotFound,
			"request-id",
		),
	}

	for provider, providerErr := range tests {
		t.Run(provider, func(t *testing.T) {
			err := normalizeObjectReadError(providerErr)
			assert.ErrorIs(t, err, fs.ErrNotExist)
			assert.ErrorIs(t, err, providerErr)
		})
	}
}

func TestNormalizeObjectReadErrorKeepsTransientErrorsDistinct(t *testing.T) {
	tests := map[string]error{
		"transport": errors.New("connection reset"),
		"minio":     minio.ErrorResponse{Code: "InternalError", StatusCode: http.StatusInternalServerError},
		"s3":        &smithy.GenericAPIError{Code: "InternalError", Message: "retry"},
		"obs":       &smithy.GenericAPIError{Code: "InternalError", Message: "retry"},
		"cos": &cos.ErrorResponse{
			Code:     "InternalError",
			Response: &http.Response{StatusCode: http.StatusInternalServerError},
		},
		"tos": &tos.TosServerError{
			RequestInfo: tos.RequestInfo{StatusCode: http.StatusInternalServerError},
			Code:        "InternalError",
		},
		"oss": &aliyunoss.ServiceError{Code: "InternalError", StatusCode: http.StatusInternalServerError},
		"ks3": ks3awserr.NewRequestFailure(
			ks3awserr.New("InternalError", "retry", nil),
			http.StatusInternalServerError,
			"request-id",
		),
	}

	for provider, providerErr := range tests {
		t.Run(provider, func(t *testing.T) {
			err := normalizeObjectReadError(providerErr)
			assert.False(t, errors.Is(err, fs.ErrNotExist))
			assert.ErrorIs(t, err, providerErr)
		})
	}
}

func TestNormalizeObjectReadErrorRequiresObjectAbsenceSemantics(t *testing.T) {
	tests := []struct {
		name         string
		code         string
		status       int
		wantNotExist bool
	}{
		{name: "object code", code: "NoSuchKey", status: http.StatusNotFound, wantNotExist: true},
		{name: "object code without status", code: "NoSuchObject", wantNotExist: true},
		{name: "bucket missing", code: "NoSuchBucket", status: http.StatusNotFound},
		{name: "unknown 404", status: http.StatusNotFound},
		{name: "access masked 404", code: "AccessDenied", status: http.StatusNotFound},
		{name: "generic not found code", code: "NotFound", status: http.StatusNotFound},
		{name: "server error", code: "InternalError", status: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			providerErr := &genericClassifierError{code: test.code, status: test.status}
			err := normalizeObjectReadError(providerErr)
			assert.Equal(t, test.wantNotExist, errors.Is(err, fs.ErrNotExist))
			assert.ErrorIs(t, err, providerErr)
		})
	}
}

func TestNormalizingObjectReaderNormalizesLazyAbsence(t *testing.T) {
	tests := []struct {
		name         string
		providerErr  error
		wantNotExist bool
	}{
		{
			name:         "object missing",
			providerErr:  minio.ErrorResponse{Code: "NoSuchKey", StatusCode: http.StatusNotFound},
			wantNotExist: true,
		},
		{
			name:        "unknown 404",
			providerErr: &genericClassifierError{status: http.StatusNotFound},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := normalizeObjectReadCloser(&fileErrorReadCloser{readErr: test.providerErr})
			_, err := reader.Read(make([]byte, 1))
			require.Error(t, err)
			assert.Equal(t, test.wantNotExist, errors.Is(err, fs.ErrNotExist))
			assert.ErrorIs(t, err, test.providerErr)
			require.NoError(t, reader.Close())
		})
	}
}

type genericClassifierError struct {
	code   string
	status int
}

func (e *genericClassifierError) Error() string       { return "generic provider error" }
func (e *genericClassifierError) ErrorCode() string   { return e.code }
func (e *genericClassifierError) HTTPStatusCode() int { return e.status }

type fileErrorReadCloser struct{ readErr error }

func (r *fileErrorReadCloser) Read([]byte) (int, error) { return 0, r.readErr }
func (r *fileErrorReadCloser) Close() error             { return nil }

var _ io.ReadCloser = (*fileErrorReadCloser)(nil)
