package session

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encodedAttachment(data string, declaredSize int64) AttachmentUpload {
	return AttachmentUpload{
		Data:     base64.StdEncoding.EncodeToString([]byte(data)),
		FileName: "file.txt",
		FileSize: declaredSize,
	}
}

func TestDecodeAndValidateAttachmentUploadsUsesActualBytes(t *testing.T) {
	uploads := []AttachmentUpload{encodedAttachment("123456", 1)}

	_, err := decodeAndValidateAttachmentUploads(uploads, 5, 5, 100)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds size limit")
}

func TestDecodeAndValidateAttachmentUploadsBoundsCountAndTotal(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		uploads := make([]AttachmentUpload, 6)
		for i := range uploads {
			uploads[i] = encodedAttachment("x", 1)
		}
		_, err := decodeAndValidateAttachmentUploads(uploads, 5, 10, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at most 5")
	})

	t.Run("total", func(t *testing.T) {
		uploads := []AttachmentUpload{
			encodedAttachment(strings.Repeat("a", 6), 6),
			encodedAttachment(strings.Repeat("b", 6), 6),
		}
		_, err := decodeAndValidateAttachmentUploads(uploads, 5, 10, 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "total request limit")
	})
}

func TestDecodeAndValidateAttachmentUploadsReturnsDecodedBytes(t *testing.T) {
	decoded, err := decodeAndValidateAttachmentUploads(
		[]AttachmentUpload{encodedAttachment("hello", 999)},
		5,
		10,
		10,
	)

	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, []byte("hello"), decoded[0])
}
