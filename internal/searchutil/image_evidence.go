package searchutil

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
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
	return len(r.MatchedImages) > 0 || r.ChunkType == string(types.ChunkTypeImageVector) ||
		r.Metadata[types.MetadataImageVectorMatch] == "true" ||
		r.Metadata[types.MetadataKeptBy] == types.KeptByImageVector
}

// InheritImageEvidence marks kept as resting on an image vector match when
// dropped, a copy de-duplication removed in its favour, did. An image hit and
// its caption hit carry the same text once merged, so either can be the one
// kept, and the image must not go with the dropped one.
func InheritImageEvidence(kept, dropped *types.SearchResult) {
	if kept == nil || !IsImageEvidence(dropped) {
		return
	}
	CaptureImageEvidence(kept)
	CaptureImageEvidence(dropped)
	kept.MatchedImages = slices.Clone(kept.MatchedImages)
	for _, image := range dropped.MatchedImages {
		if !slices.Contains(kept.MatchedImages, image) {
			kept.MatchedImages = append(kept.MatchedImages, image)
		}
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

// CaptureImageEvidence freezes the original image address while ImageInfo still
// belongs to the image hit. Call it before any merge rewrites ImageInfo.
func CaptureImageEvidence(r *types.SearchResult) {
	if r == nil || len(r.MatchedImages) > 0 || r.ChunkType != string(types.ChunkTypeImageVector) {
		return
	}
	// Older stored references may already contain expanded parent images.
	// Without a captured identity, guessing their first image is unsafe.
	if r.MatchType == types.MatchTypeHistory {
		return
	}
	var infos []types.ImageInfo
	if json.Unmarshal([]byte(r.ImageInfo), &infos) != nil || len(infos) == 0 {
		return
	}
	url := strings.TrimSpace(infos[0].URL)
	if url == "" {
		url = strings.TrimSpace(infos[0].OriginalURL)
	}
	if url != "" {
		r.MatchedImages = []types.MatchedImage{{ChunkID: r.ID, KnowledgeBaseID: r.KnowledgeBaseID, URL: url}}
	}
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
	seen := make(map[struct{ kb, url string }]bool)
	for i, r := range results {
		if !IsImageEvidence(r) {
			continue
		}
		CaptureImageEvidence(r)
		for _, ref := range r.MatchedImages {
			if len(images) >= maxImages {
				return images, positions
			}
			key := struct{ kb, url string }{ref.KnowledgeBaseID, ref.URL}
			if ref.URL == "" || seen[key] {
				continue
			}
			seen[key] = true
			// Use the matched image's own storage and chunk identity even if a
			// duplicate from another document/KB now carries its evidence.
			source := *r
			source.ID, source.KnowledgeBaseID = ref.ChunkID, ref.KnowledgeBaseID
			info, _ := json.Marshal([]types.ImageInfo{{URL: ref.URL}})
			source.ImageInfo = string(info)
			data, err := read(ctx, &source)
			if err != nil {
				logger.Warnf(ctx, "[ContextImages] Image of %s unreadable: %v", ref.ChunkID, err)
				continue
			}
			img, err := imageprep.Prepare(data, VisionImageLimits)
			if err != nil {
				logger.Warnf(ctx, "[ContextImages] Image of %s does not fit a vision model: %v", ref.ChunkID, err)
				continue
			}
			images = append(images, img.DataURI())
			positions = append(positions, i)
		}
	}
	return images, positions
}
