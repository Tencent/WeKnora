package file

import (
	"errors"
	"io"
	"io/fs"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

func normalizeObjectReadError(err error) error {
	if err == nil || errors.Is(err, fs.ErrNotExist) || !isObjectNotFoundError(err) {
		return err
	}
	return errors.Join(fs.ErrNotExist, err)
}

func isObjectNotFoundError(err error) bool {
	switch strings.ToLower(objectErrorCode(err)) {
	case "nosuchkey", "nosuchobject", "objectnotfound", "nosuchfile":
		return true
	default:
		return false
	}
}

func objectErrorCode(err error) string {
	var minioErrorPointer *minio.ErrorResponse
	if errors.As(err, &minioErrorPointer) {
		return minioErrorPointer.Code
	}
	var minioError minio.ErrorResponse
	if errors.As(err, &minioError) {
		return minioError.Code
	}
	var cosError *cos.ErrorResponse
	if errors.As(err, &cosError) {
		return cosError.Code
	}
	var tosError *tos.TosServerError
	if errors.As(err, &tosError) {
		return tosError.Code
	}
	var errorCoder interface{ ErrorCode() string }
	if errors.As(err, &errorCoder) {
		return errorCoder.ErrorCode()
	}
	var coder interface{ Code() string }
	if errors.As(err, &coder) {
		return coder.Code()
	}
	return ""
}

type normalizingObjectReadCloser struct{ io.ReadCloser }

func (r normalizingObjectReadCloser) Read(data []byte) (int, error) {
	n, err := r.ReadCloser.Read(data)
	return n, normalizeObjectReadError(err)
}

func normalizeObjectReadCloser(reader io.ReadCloser) io.ReadCloser {
	return normalizingObjectReadCloser{ReadCloser: reader}
}
