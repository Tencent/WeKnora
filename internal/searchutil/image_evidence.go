package searchutil

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/types"
)

// MaxContextImages caps the retrieved images shown to a vision chat model in
// one answer: each costs as much context as a long passage.
const MaxContextImages = 3

// VisionImageLimits are what every vision chat API this build talks to takes:
// PNG, JPEG, WebP and GIF, and Anthropic's 5 MB per image, the smallest of
// the documented limits (OpenAI and Gemini allow 20 MB).
var VisionImageLimits = imageprep.Limits{
	MaxBytes:  5_000_000,
	MIMETypes: []string{"image/png", "image/jpeg", "image/webp", "image/gif"},
}

// IsImageEvidence reports whether a result rests on an image matched by its
// own vector: an image_vector hit, or the copy that stands in for one.
func IsImageEvidence(r *types.SearchResult) bool {
	if r == nil {
		return false
	}
	return r.ChunkType == string(types.ChunkTypeImageVector) ||
		r.Metadata[types.MetadataImageVectorMatch] == "true" ||
		r.Metadata[types.MetadataKeptBy] == types.KeptByImageVector
}

// InheritImageEvidence marks kept as resting on an image vector match when
// dropped, a copy de-duplication removed in its favour, did. An image hit and
// its caption hit carry the same text once merged, so either can be the one
// kept, and the image must not go with the dropped one.
func InheritImageEvidence(kept, dropped *types.SearchResult) {
	if kept == nil || !IsImageEvidence(dropped) || IsImageEvidence(kept) {
		return
	}
	kept.Metadata = maps.Clone(kept.Metadata)
	if kept.Metadata == nil {
		kept.Metadata = make(map[string]string, 1)
	}
	kept.Metadata[types.MetadataImageVectorMatch] = "true"
}

// IsKeptOutsideTopK reports a result kept for a reason other than its score,
// which a top-k cut must not drop. See types.MetadataKeptBy.
func IsKeptOutsideTopK(r *types.SearchResult) bool {
	return r != nil && r.Metadata[types.MetadataKeptBy] != ""
}

// TopKKeepingKept returns the first k of the ranked results, followed by the
// results kept outside top-k, each group in its input order.
func TopKKeepingKept(results []*types.SearchResult, k int) []*types.SearchResult {
	var ranked, kept []*types.SearchResult
	for _, r := range results {
		if IsKeptOutsideTopK(r) {
			kept = append(kept, r)
		} else {
			ranked = append(ranked, r)
		}
	}
	if k >= 0 && len(ranked) > k {
		ranked = ranked[:k]
	}
	return append(ranked, kept...)
}

// firstImageURL is the address of a result's first image, which identifies
// it across the copies of one image on several chunks.
func firstImageURL(r *types.SearchResult) string {
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(r.ImageInfo), &infos); err != nil || len(infos) == 0 {
		return ""
	}
	if u := strings.TrimSpace(infos[0].URL); u != "" {
		return u
	}
	return strings.TrimSpace(infos[0].OriginalURL)
}

// ContextImages reads the images of the image-evidence results for a vision
// chat model: at most maxImages distinct images, in result order, prepared to
// VisionImageLimits, as data URIs. positions are the indices into results the
// images belong to. An image that cannot be read or prepared is skipped; the
// model still has its caption.
func ContextImages(
	ctx context.Context, results []*types.SearchResult,
	read func(context.Context, *types.SearchResult) ([]byte, error), maxImages int,
) (images []string, positions []int) {
	if read == nil || maxImages <= 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	for i, r := range results {
		if len(images) >= maxImages {
			break
		}
		if !IsImageEvidence(r) {
			continue
		}
		url := firstImageURL(r)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		data, err := read(ctx, r)
		if err != nil {
			logger.Warnf(ctx, "[ContextImages] Image of %s unreadable: %v", r.ID, err)
			continue
		}
		img, err := imageprep.Prepare(data, VisionImageLimits)
		if err != nil {
			logger.Warnf(ctx, "[ContextImages] Image of %s does not fit a vision model: %v", r.ID, err)
			continue
		}
		images = append(images, img.DataURI())
		positions = append(positions, i)
	}
	return images, positions
}
