package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type imageReadFunc func(context.Context, string, func(*types.Knowledge) bool) ([]byte, *types.Knowledge, error)

func (f imageReadFunc) ReadImage(
	ctx context.Context, ref string, authorize func(*types.Knowledge) bool,
) ([]byte, *types.Knowledge, error) {
	return f(ctx, ref, authorize)
}

type artifactReadError struct{ err error }

func (s artifactReadError) Read(context.Context, string) ([]byte, error) { return nil, s.err }

func TestReadFileKnowledgeImageFallback(t *testing.T) {
	ref := types.ResourceScheme + strings.Repeat("a", types.ResourceHandleLength)
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb"}}
	owner := &types.Knowledge{ID: "doc", KnowledgeBaseID: "kb"}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	oversizedPNG := bytes.Clone(encoded.Bytes())
	// Change only IHDR (width, height and checksum) without allocating a giant image.
	binary.BigEndian.PutUint32(oversizedPNG[16:20], 10_000)
	binary.BigEndian.PutUint32(oversizedPNG[20:24], 10_000)
	binary.BigEndian.PutUint32(oversizedPNG[29:33], crc32.ChecksumIEEE(oversizedPNG[12:29]))

	for _, tc := range []struct {
		name, path, wantError string
		artifactErr           error
		image                 []byte
		wantRead              bool
	}{
		{
			name: "artifact read error", path: ref, artifactErr: errors.New("file exceeds the limit"),
			wantError: "file exceeds the limit",
		},
		{
			name: "sandbox alias", path: "sandbox:foo.jpg", artifactErr: ErrArtifactUnavailable,
			wantError: ErrArtifactUnavailable.Error(),
		},
		{
			name: "malformed handle", path: ref + "/more", artifactErr: ErrArtifactUnavailable,
			wantError: "file is unavailable in this conversation or knowledge scope",
		},
		{
			name: "invalid image", path: ref, image: []byte("not a JPEG"), wantRead: true,
			wantError: "the stored knowledge image is invalid or too large to inspect",
		},
		{
			name: "oversized image", path: ref, image: oversizedPNG, wantRead: true,
			wantError: "the stored knowledge image is invalid or too large to inspect",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			source := imageReadFunc(func(_ context.Context, got string, authorize func(*types.Knowledge) bool) (
				[]byte, *types.Knowledge, error,
			) {
				called = true
				require.Equal(t, ref, got)
				require.True(t, authorize(owner))
				return tc.image, owner, nil
			})
			reader := NewReadFileTool(nil).WithKnowledgeImages(source, targets, struct{ interfaces.KnowledgeService }{})
			if tc.artifactErr != nil {
				reader.WithArtifacts(artifactReadError{err: tc.artifactErr})
			}
			args, err := json.Marshal(ReadFileInput{Path: tc.path})
			require.NoError(t, err)
			result, err := reader.Execute(t.Context(), args)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Equal(t, tc.wantError, result.Error)
			require.Empty(t, result.Images)
			require.Equal(t, tc.wantRead, called)
		})
	}
}
