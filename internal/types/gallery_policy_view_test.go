package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveGalleryConfigViewDefaults(t *testing.T) {
	// No user tier: waterfall layout at the default column width.
	res := ResolveGalleryConfig("kb-1", nil, nil, nil)
	require.Equal(t, GalleryViewWaterfall, res.ViewMode)
	require.Equal(t, GalleryThumbWidthDefault, res.ThumbWidth)
}

func TestResolveGalleryConfigViewUserTier(t *testing.T) {
	// Recorded user state is served back verbatim.
	res := ResolveGalleryConfig("kb-1", nil, nil, &GalleryPolicyTier{
		ViewMode:   GalleryViewCard,
		ThumbWidth: 280,
	})
	require.Equal(t, GalleryViewCard, res.ViewMode)
	require.Equal(t, 280, res.ThumbWidth)
}

func TestResolveGalleryConfigViewInvalidFallback(t *testing.T) {
	// Unknown layout and out-of-range width fall back to the defaults so
	// a stale or hand-edited record cannot break rendering.
	res := ResolveGalleryConfig("kb-1", nil, nil, &GalleryPolicyTier{
		ViewMode:   "grid",
		ThumbWidth: 9999,
	})
	require.Equal(t, GalleryViewWaterfall, res.ViewMode)
	require.Equal(t, GalleryThumbWidthDefault, res.ThumbWidth)
}
