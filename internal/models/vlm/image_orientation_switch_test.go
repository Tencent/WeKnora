package vlm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrientationIsOptIn pins the switch and its default.
//
// The pass is off unless the deployment asks for it, the same rule the other
// image-pipeline additions follow: honouring an EXIF tag is right when the tag
// is right and wrong when a camera wrote it wrong, and the two are
// indistinguishable from the bytes. Off-by-default also means an upgrade cannot
// change what happens to documents a deployment already ingests.
func TestOrientationIsOptIn(t *testing.T) {
	stored := markedJPEG(t, 6, binary.BigEndian)
	ctx := context.Background()

	t.Run("default forwards the stored bytes", func(t *testing.T) {
		// Pin it to the default explicitly: the switch reads the ambient
		// environment, so a developer (or a CI job) that exports
		// VLM_IMAGE_ORIENTATION=on would otherwise flip this case over.
		t.Setenv("VLM_IMAGE_ORIENTATION", "")
		out := orientImageBytes(ctx, stored)
		assert.True(t, bytes.Equal(stored, out),
			"the default must forward the stored bytes byte for byte")
		assert.Equal(t, 6, jpegEXIFOrientation(out),
			"the stored tag must survive untouched")
	})

	for _, value := range []string{"1", "true", "yes", "on", " ON "} {
		t.Run("enabled by "+value, func(t *testing.T) {
			t.Setenv("VLM_IMAGE_ORIENTATION", value)
			out := orientImageBytes(ctx, stored)
			require.NotEqual(t, stored, out, "the tag must be honoured once enabled")
			assert.Zero(t, jpegEXIFOrientation(out), "the re-encoded page carries no tag")
		})
	}

	for _, value := range []string{"0", "false", "no", "off", "maybe"} {
		t.Run("stays off for "+value, func(t *testing.T) {
			t.Setenv("VLM_IMAGE_ORIENTATION", value)
			out := orientImageBytes(ctx, stored)
			assert.True(t, bytes.Equal(stored, out),
				"anything but the on spellings must forward the stored bytes")
		})
	}
}
