package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGalleryViewPreferencesPersistAndPartialMerge(t *testing.T) {
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice"},
	}}
	users := &userService{userRepo: repo}
	ctx := t.Context()

	// Recording the view state persists both keys and survives the same
	// JSON round-trip as the DB column.
	prefs, err := users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{ViewMode: types.GalleryViewCard, ThumbCols: 10},
	})
	require.NoError(t, err)
	require.Equal(t, types.GalleryViewCard, prefs.Gallery.ViewMode)
	require.Equal(t, 10, prefs.Gallery.ThumbCols)
	raw, err := prefs.Value()
	require.NoError(t, err)
	var decoded types.UserPreferences
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, types.GalleryViewCard, decoded.Gallery.ViewMode)
	require.Equal(t, 10, decoded.Gallery.ThumbCols)

	// A search-prefs-only patch (what the filter panel sends) must leave
	// the view state untouched: the gallery block merges key by key.
	prefs, err = users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{Mode: types.GalleryModeCustom},
	})
	require.NoError(t, err)
	require.Equal(t, types.GalleryModeCustom, prefs.Gallery.Mode)
	require.Equal(t, types.GalleryViewCard, prefs.Gallery.ViewMode, "view mode survives a search-only patch")
	require.Equal(t, 10, prefs.Gallery.ThumbCols, "thumb column count survives a search-only patch")

	// A view-prefs-only patch must leave the search state untouched too.
	prefs, err = users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{ViewMode: types.GalleryViewWaterfall},
	})
	require.NoError(t, err)
	require.Equal(t, types.GalleryModeCustom, prefs.Gallery.Mode, "search mode survives a view-only patch")
	require.Equal(t, types.GalleryViewWaterfall, prefs.Gallery.ViewMode)
}

func TestGalleryViewPreferencesValidation(t *testing.T) {
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice"},
	}}
	users := &userService{userRepo: repo}
	ctx := t.Context()

	// Unknown view modes are ignored, not stored.
	prefs, err := users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{ViewMode: "grid"},
	})
	require.NoError(t, err)
	require.Empty(t, prefs.Gallery.ViewMode)

	// Thumb column count is clamped into the slider range on write; 0
	// means "not sent" and leaves the stored value alone.
	prefs, err = users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{ThumbCols: 9999},
	})
	require.NoError(t, err)
	require.Equal(t, types.GalleryThumbColsMax, prefs.Gallery.ThumbCols)
	prefs, err = users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{ThumbCols: 0},
	})
	require.NoError(t, err)
	require.Equal(t, 32, prefs.Gallery.ThumbCols, "0 means no key sent, so the stored count survives")
	prefs, err = users.UpdateUserPreferences(ctx, "alice", types.UserPreferences{
		Gallery: &types.GalleryUserPrefs{ThumbCols: -3},
	})
	require.NoError(t, err)
	require.Equal(t, 1, prefs.Gallery.ThumbCols, "negative values clamp to the lowest stop")
}
