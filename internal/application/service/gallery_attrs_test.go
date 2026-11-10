package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The filter semantics themselves (unobserved images pass, "on" outranks
// "off", ...) are pinned against real databases in the repository's
// ListImageAssets tests; these cases pin how a gallery request is translated.

func TestBuildImageAssetQuery_Defaults(t *testing.T) {
	q := buildImageAssetQuery(nil, &types.Pagination{Page: 3, PageSize: 24})

	require.Equal(t, types.ImageAssetField{Builtin: "created_at"}, q.SortField)
	require.True(t, q.SortDesc, "newest first")
	require.Equal(t, 48, q.Offset)
	require.Equal(t, 24, q.Limit)

	q = buildImageAssetQuery(&types.ImageListFilter{Keyword: " chart "}, &types.Pagination{})
	require.Equal(t, "chart", q.Keyword)
	require.Equal(t, []types.ImageAssetField{{Builtin: "caption"}, {Builtin: "ocr_text"}}, q.SearchFields,
		"no search_in searches caption and OCR text")
}

func TestBuildImageAssetQuery_TranslatesNamespacedIDs(t *testing.T) {
	q := buildImageAssetQuery(&types.ImageListFilter{
		SearchIn:    []string{"builtin:caption", "system:contain.text", "no-separator"},
		AttrFilters: map[string][]string{"system:contain.text": {" block ", "sparse"}},
		AttrRules: map[string]map[string]string{
			"system:contain.data_visual": {"true": "on", "false": "maybe"},
			"system:contain.text":        {"none": "off", "block": "off"},
		},
		SortBy:    "system:contain.text",
		SortOrder: "asc",
	}, &types.Pagination{})

	text := types.ImageAssetField{Attr: "contain.text"}
	visual := types.ImageAssetField{Attr: "contain.data_visual"}
	require.Equal(t, []types.ImageAssetField{{Builtin: "caption"}, text}, q.SearchFields,
		"ids without a source are dropped")
	require.Equal(t, []types.ImageAssetValueSet{{Field: text, Values: []string{"block", "sparse"}}}, q.AttrFilters)
	require.Equal(t, []types.ImageAssetValueSet{{Field: visual, Values: []string{"true"}}}, q.OnRules,
		"unknown verdicts are ignored")
	require.Equal(t, []types.ImageAssetValueSet{{Field: text, Values: []string{"block", "none"}}}, q.OffRules,
		"values are sorted so one request always yields one statement")
	require.Equal(t, text, q.SortField)
	require.False(t, q.SortDesc)
}

func TestBuildImageAssetQuery_LegacySortNames(t *testing.T) {
	for _, name := range []string{"created_at", "updated_at", "caption"} {
		q := buildImageAssetQuery(&types.ImageListFilter{SortBy: name}, &types.Pagination{})
		require.Equal(t, types.ImageAssetField{Builtin: name}, q.SortField, name)
	}
}
