package session

import (
	"encoding/base64"
	"fmt"
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

func TestInlineAttachmentUploadCountSetting(t *testing.T) {
	for _, tc := range []struct {
		value string
		limit int
	}{
		{"", 5},
		{"invalid", 5},
		{"0", 5},
		{"-2", 5},
		{"2", 2},
		{"8", 8},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("MAX_ATTACHMENT_UPLOADS_PER_REQUEST", tc.value)
			uploads := make([]AttachmentUpload, tc.limit+1)
			for i := range uploads {
				uploads[i] = encodedAttachment("x", 1)
			}

			_, err := decodeAndValidateAttachmentUploads(uploads[:tc.limit], maxAttachmentUploadsPerRequest(), 10, 100)
			require.NoError(t, err)
			_, err = decodeAndValidateAttachmentUploads(uploads, maxAttachmentUploadsPerRequest(), 10, 100)
			require.EqualError(t, err, fmt.Sprintf("at most %d attachments are allowed per request", tc.limit))
		})
	}
}
