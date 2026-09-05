package file

import (
	"errors"
	"os"

	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/smithy-go"
	ks3awserr "github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/minio/minio-go/v7"
	cos "github.com/tencentyun/cos-go-sdk-v5"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

// ErrCrossBackendCopy is returned by CopyFile implementations when the source
// path belongs to a different storage provider than the destination service.
// PR1 only supports same-backend (server-side) copies; cross-backend streaming
// copy is intentionally not implemented yet.
var ErrCrossBackendCopy = errors.New("file: cross-backend copy not supported")

// IsFileNotFound reports whether err explicitly identifies a missing object.
// HTTP status alone is deliberately insufficient because a missing bucket can
// also produce 404, while permission and transport errors must remain visible.
func IsFileNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}

	var smithyErr smithy.APIError
	if errors.As(err, &smithyErr) && isSmithyObjectNotFoundCode(smithyErr.ErrorCode()) {
		return true
	}

	var ks3Err ks3awserr.Error
	if errors.As(err, &ks3Err) && isObjectNotFoundCode(ks3Err.Code()) {
		return true
	}

	var minioErr minio.ErrorResponse
	if errors.As(err, &minioErr) && isObjectNotFoundCode(minioErr.Code) {
		return true
	}
	var minioErrPtr *minio.ErrorResponse
	if errors.As(err, &minioErrPtr) && minioErrPtr != nil && isObjectNotFoundCode(minioErrPtr.Code) {
		return true
	}

	var cosErr *cos.ErrorResponse
	if errors.As(err, &cosErr) && cosErr != nil && isObjectNotFoundCode(cosErr.Code) {
		return true
	}

	var ossErr *oss.ServiceError
	if errors.As(err, &ossErr) && ossErr != nil && isObjectNotFoundCode(ossErr.Code) {
		return true
	}

	var tosErr *tos.TosServerError
	return errors.As(err, &tosErr) && tosErr != nil && isObjectNotFoundCode(tosErr.Code)
}

func isObjectNotFoundCode(code string) bool {
	return code == "NoSuchKey"
}

func isSmithyObjectNotFoundCode(code string) bool {
	return isObjectNotFoundCode(code) || code == "NotFound"
}
