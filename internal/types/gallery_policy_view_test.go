package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveGalleryConfigViewDefaults(t *testing.T) {
	// No user tier: waterfall layout and no recorded column count — the
	// client derives a count from its own layout width.
	res := ResolveGalleryConfig("kb-1", nil, nil, nil)
	require.Equal(t, GalleryViewWaterfall, res.ViewMode)
	require.Equal(t, 0, res.ThumbCols)
}

func TestResolveGalleryConfigViewUserTier(t *testing.T) {
	// Recorded user state is served back verbatim.
	res := ResolveGalleryConfig("kb-1", nil, nil, &GalleryPolicyTier{
		ViewMode:  GalleryViewCard,
		ThumbCols: 6,
	})
	require.Equal(t, GalleryViewCard, res.ViewMode)
	require.Equal(t, 6, res.ThumbCols)
}

func TestResolveGalleryConfigViewInvalidFallback(t *testing.T) {
	// Unknown layout and out-of-range column counts fall back to the
	// defaults so a stale or hand-edited record cannot break rendering.
	res := ResolveGalleryConfig("kb-1", nil, nil, &GalleryPolicyTier{
		ViewMode:  "grid",
		ThumbCols: 9999,
	})
	require.Equal(t, GalleryViewWaterfall, res.ViewMode)
	require.Equal(t, 0, res.ThumbCols)
}
