package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/minio/minio-go/v7"
	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DeleteAndVerifyAbsent deletes and checks the same physical path on one
// resolved backend. A retry succeeds if the provider confirms the object is
// gone even when its delete operation failed or timed out.
func DeleteAndVerifyAbsent(ctx context.Context, backend interfaces.FileService, path string) error {
	deleteErr := backend.DeleteFile(ctx, path)
	reader, err := backend.GetFile(ctx, path)
	if err == nil {
		if reader == nil {
			return errors.Join(deleteErr, fmt.Errorf("storage returned no read result"))
		}
		defer reader.Close()
		// MinIO opens objects lazily. EOF also proves an empty object exists.
		_, err = io.ReadFull(reader, make([]byte, 1))
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.Join(deleteErr, fmt.Errorf("original object still exists"))
		}
	}
	if originalObjectMissing(err) {
		return nil
	}
	return errors.Join(deleteErr, fmt.Errorf("verify original object absence: %w", err))
}

func originalObjectMissing(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	// S3, OBS and OSS expose an explicit error code. Neither a bare 404 nor
	// a missing bucket demonstrates that this specific object was deleted.
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode() == "NoSuchKey"
	}
	var minioErr minio.ErrorResponse
	if errors.As(err, &minioErr) {
		return minioErr.Code == "NoSuchKey"
	}
	var cosErr *cos.ErrorResponse
	if errors.As(err, &cosErr) {
		return cosErr.Response != nil && cosErr.Response.StatusCode == http.StatusNotFound && cosErr.Code == "NoSuchKey"
	}
	var ks3Err awserr.Error
	if errors.As(err, &ks3Err) {
		return ks3Err.Code() == "NoSuchKey"
	}
	var tosErr *tos.TosServerError
	return errors.As(err, &tosErr) && tosErr.StatusCode == http.StatusNotFound && tosErr.Code == "NoSuchKey"
}
