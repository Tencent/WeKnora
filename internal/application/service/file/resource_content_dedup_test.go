package file

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// contentCatalogStub is an in-memory stand-in for the resource catalog that
// answers content-hash lookups the way the real one does: the first payload
// registered under a hash owns the handle for every later copy of those bytes.
// Register deliberately mints a fresh handle per call, since the production
// location-based dedup would otherwise mask the content-hash path under test.
type contentCatalogStub struct {
	byHash     map[string]string
	registered []interfaces.ResourceRegistration
	reuseCalls int
	reuseErr   error
}

func newContentCatalogStub() *contentCatalogStub {
	return &contentCatalogStub{byHash: map[string]string{}}
}

func (c *contentCatalogStub) Register(
	_ context.Context,
	_ uint64,
	_ string,
	meta interfaces.ResourceRegistration,
) (string, error) {
	// "Handle" + 16 digits is exactly types.ResourceHandleLength characters.
	ref := types.BuildResourcePath(fmt.Sprintf("Handle%016d", len(c.registered)))
	c.registered = append(c.registered, meta)
	if meta.ContentHash != "" {
		if _, exists := c.byHash[meta.ContentHash]; !exists {
			c.byHash[meta.ContentHash] = ref
		}
	}
	return ref, nil
}

func (c *contentCatalogStub) ReuseByContentHash(
	_ context.Context, _ uint64, contentHash string,
) (string, bool, error) {
	c.reuseCalls++
	if c.reuseErr != nil {
		return "", false, c.reuseErr
	}
	ref, ok := c.byHash[contentHash]
	return ref, ok, nil
}

func (c *contentCatalogStub) Resolve(context.Context, string) (*types.StoredResource, error) {
	return nil, nil
}

func (c *contentCatalogStub) ResolvePath(
	_ context.Context, value string,
) (string, *types.StoredResource, error) {
	return value, nil, nil
}

func (c *contentCatalogStub) Bind(context.Context, string, string, string, string) error { return nil }
func (c *contentCatalogStub) MarkDeleted(context.Context, string) error                  { return nil }

func (c *contentCatalogStub) Release(context.Context, string, string, string) (int64, error) {
	return 0, nil
}

func (c *contentCatalogStub) ListReferencesByOwner(context.Context, string, ...string) ([]string, error) {
	return nil, nil
}

func (c *contentCatalogStub) CreateAccessGrant(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (c *contentCatalogStub) ResolveAccessGrant(context.Context, string) (*types.StoredResource, error) {
	return nil, nil
}

// countingFileStub counts how many payloads actually reached the provider, which
// is the observable effect of a reuse: the bytes are never written.
type countingFileStub struct {
	physicalFileStub
	saveBytesCalls int
}

func (s *countingFileStub) SaveBytes(_ context.Context, _ []byte, _ uint64, _ string, _ bool) (string, error) {
	s.saveBytesCalls++
	return fmt.Sprintf("local://7/exports/img-%d.bin", s.saveBytesCalls), nil
}

// pngPayload starts with the PNG signature so http.DetectContentType classifies
// the bytes as an image regardless of the name they are saved under.
func pngPayload() []byte {
	return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
}

func TestResourceCatalogFileServiceReusesIdenticalImageBytes(t *testing.T) {
	ctx := context.Background()
	inner := &countingFileStub{}
	catalog := newContentCatalogStub()
	svc := NewResourceCatalogFileService(inner, catalog)

	first, err := svc.SaveBytes(ctx, pngPayload(), 7, "page-1.png", false)
	require.NoError(t, err)
	// The second occurrence carries the generated name an extractor would give
	// the same logo on another page: different name, identical bytes.
	second, err := svc.SaveBytes(ctx, pngPayload(), 7, "page-2.png", false)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, inner.saveBytesCalls, "identical bytes must be written once")
	require.Len(t, catalog.registered, 1, "the reused payload must not be registered twice")
	require.Equal(t, int64(len(pngPayload())), catalog.registered[0].Size)
	require.NotEmpty(t, catalog.registered[0].ContentHash)
}

func TestResourceCatalogFileServiceWritesDistinctImageBytesSeparately(t *testing.T) {
	ctx := context.Background()
	inner := &countingFileStub{}
	catalog := newContentCatalogStub()
	svc := NewResourceCatalogFileService(inner, catalog)

	first, err := svc.SaveBytes(ctx, pngPayload(), 7, "page-1.png", false)
	require.NoError(t, err)
	second, err := svc.SaveBytes(ctx, append(pngPayload(), 0x00), 7, "page-2.png", false)
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.Equal(t, 2, inner.saveBytesCalls)
	require.Len(t, catalog.registered, 2)
}

func TestResourceCatalogFileServiceDoesNotReuseTemporaryBytes(t *testing.T) {
	ctx := context.Background()
	inner := &countingFileStub{}
	catalog := newContentCatalogStub()
	svc := NewResourceCatalogFileService(inner, catalog)

	first, err := svc.SaveBytes(ctx, pngPayload(), 7, "scratch-1.png", true)
	require.NoError(t, err)
	second, err := svc.SaveBytes(ctx, pngPayload(), 7, "scratch-2.png", true)
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.Equal(t, 2, inner.saveBytesCalls)
	require.Zero(t, catalog.reuseCalls, "temporary writes must not consult the content index")
}

// Non-image payloads keep their own registered object even when their bytes are
// equal: for an upload or an artifact the original name is part of the result,
// and sharing would make the second file answer to the first one's name.
func TestResourceCatalogFileServiceDoesNotReuseNonImageBytes(t *testing.T) {
	ctx := context.Background()
	inner := &countingFileStub{}
	catalog := newContentCatalogStub()
	svc := NewResourceCatalogFileService(inner, catalog)
	notes := []byte("quarterly notes\n")

	first, err := svc.SaveBytes(ctx, notes, 7, "notes-a.txt", false)
	require.NoError(t, err)
	second, err := svc.SaveBytes(ctx, notes, 7, "notes-b.txt", false)
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.Equal(t, 2, inner.saveBytesCalls)
	require.Zero(t, catalog.reuseCalls, "non-image payloads must not consult the content index")
}

// The lookup is an optimisation, not a gate: if it fails, the bytes still have
// to be stored.
func TestResourceCatalogFileServiceStoresWhenContentLookupFails(t *testing.T) {
	ctx := context.Background()
	inner := &countingFileStub{}
	catalog := newContentCatalogStub()
	catalog.reuseErr = errors.New("content index unavailable")
	svc := NewResourceCatalogFileService(inner, catalog)

	first, err := svc.SaveBytes(ctx, pngPayload(), 7, "page-1.png", false)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	require.Equal(t, 1, inner.saveBytesCalls)
	require.Equal(t, 1, catalog.reuseCalls)
	require.Len(t, catalog.registered, 1, "a failed lookup must fall back to the write path")
}

// The image/non-image boundary is what keeps payload sharing from leaking into
// uploads and artifacts, so pin it down directly.
func TestIsImagePayloadKeepsNameBoundary(t *testing.T) {
	cases := []struct {
		label string
		name  string
		data  []byte
		want  bool
	}{
		{"sniffed png under an image name", "page-1.png", pngPayload(), true},
		{"sniffed png with no extension", "no-extension", pngPayload(), true},
		{"svg relies on its extension", "logo.svg", []byte("<svg/>"), true},
		{"plain text", "notes.txt", []byte("hello"), false},
		{"markdown artifact", "web-1.md", []byte("# title\n"), false},
		{"unrecognised binary", "blob.bin", []byte{0x00, 0x01, 0x02, 0x03}, false},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, isImagePayload(tc.name, tc.data), "case %s", tc.label)
	}
}
