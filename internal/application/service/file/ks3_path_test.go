package file

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bucket is not part of the KS3 object key, but the tenant check scans it;
// a numeric bucket segment must not pass as the caller's tenant.
func TestKS3ObjectKeyRequiresTheServiceBucket(t *testing.T) {
	svc := &ks3FileService{bucketName: "weknora"}
	_, err := svc.objectKey("ks3://42/prefix/7/knowledge/secret.pdf")
	require.Error(t, err)

	key, err := svc.objectKey("ks3://weknora/prefix/42/exports/a.png")
	require.NoError(t, err)
	assert.Equal(t, "prefix/42/exports/a.png", key)
}

// A server-side copy reads only this service's bucket; another bucket's path
// is reported as a cross-backend copy so the caller streams it instead.
func TestKS3CopyFileRejectsForeignBucket(t *testing.T) {
	svc := &ks3FileService{bucketName: "weknora"}
	_, err := svc.CopyFile(context.Background(), "ks3://other-bucket/prefix/7/knowledge/a.pdf", 42, "k-1")
	require.True(t, errors.Is(err, ErrCrossBackendCopy), "err = %v", err)
}
