package file

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCosObjectName_RejectsLocalScheme(t *testing.T) {
	svc := &cosFileService{bucketURL: "https://b.cos.ap-shanghai.myqcloud.com/"}
	_, err := svc.parseCosObjectName("local://10000/exports/img.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local")
}

func TestParseCosObjectName_CosScheme(t *testing.T) {
	svc := &cosFileService{bucketURL: "https://b.cos.ap-shanghai.myqcloud.com/"}
	key, err := svc.parseCosObjectName("cos://bucket/ap-shanghai/weknora/10000/exports/a.png")
	require.NoError(t, err)
	assert.Equal(t, "weknora/10000/exports/a.png", key)
}

func TestParseCosObjectName_RejectsMinioScheme(t *testing.T) {
	svc := &cosFileService{bucketURL: "https://b.cos.ap-shanghai.myqcloud.com/"}
	_, err := svc.parseCosObjectName("minio://wizard-test/10000/exports/img.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minio")
}
