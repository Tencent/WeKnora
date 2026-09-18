package file

import (
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
