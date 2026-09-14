package service

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
)

// Multimodal image filtering thresholds. They keep tiny decorative images,
// long separator strips, and image-only PDFs with hundreds of figures from
// monopolizing the VLM queue. All three are env-tunable; a value of 0 (or an
// invalid/unset value) falls back to the default, and the defaults are chosen
// so typical documents pass through unfiltered.
const (
	defaultMultimodalMaxImagesPerDocument = 256
	defaultMultimodalMinImageSide         = 32
	defaultMultimodalMaxImageAspectRatio  = 20.0
)

func multimodalMaxImagesPerDocument() int {
	return envInt("WEKNORA_MULTIMODAL_MAX_IMAGES_PER_DOCUMENT", defaultMultimodalMaxImagesPerDocument)
}

func multimodalMinImageSide() int {
	return envInt("WEKNORA_MULTIMODAL_MIN_IMAGE_SIDE", defaultMultimodalMinImageSide)
}

func multimodalMaxImageAspectRatio() float64 {
	return envFloat("WEKNORA_MULTIMODAL_MAX_IMAGE_ASPECT_RATIO", defaultMultimodalMaxImageAspectRatio)
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envFloat(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// filterMultimodalImages drops stored images that are not worth a VLM call:
// images with a side smaller than the minimum (icons, decorative bullets),
// images with an extreme aspect ratio (separator strips), and everything
// beyond the per-document cap. Images with unknown dimensions (Width/Height
// of 0, i.e. undecodable at store time) are kept: dropping content we could
// not measure would risk losing real figures.
func filterMultimodalImages(ctx context.Context, images []docparser.StoredImage) []docparser.StoredImage {
	if len(images) == 0 {
		return nil
	}

	maxImages := multimodalMaxImagesPerDocument()
	minSide := multimodalMinImageSide()
	maxAspectRatio := multimodalMaxImageAspectRatio()
	filtered := make([]docparser.StoredImage, 0, len(images))
	skippedSmall := 0
	skippedAspect := 0
	skippedLimit := 0

	for _, img := range images {
		skip, reason := shouldSkipMultimodalImage(img, minSide, maxAspectRatio)
		if skip {
			switch reason {
			case "small_side":
				skippedSmall++
			case "extreme_aspect_ratio":
				skippedAspect++
			}
			continue
		}
		if maxImages > 0 && len(filtered) >= maxImages {
			skippedLimit++
			continue
		}
		filtered = append(filtered, img)
	}

	if skippedSmall > 0 || skippedAspect > 0 || skippedLimit > 0 {
		logger.Infof(ctx,
			"[KnowledgeProcess] Filtered multimodal images: source=%d kept=%d small_side=%d extreme_aspect=%d over_limit=%d max_images=%d min_side=%d max_aspect=%.2f",
			len(images), len(filtered), skippedSmall, skippedAspect, skippedLimit, maxImages, minSide, maxAspectRatio)
	}
	return filtered
}

// shouldSkipMultimodalImage reports whether img should be excluded from
// multimodal processing under the given thresholds, and why.
func shouldSkipMultimodalImage(img docparser.StoredImage, minSide int, maxAspectRatio float64) (bool, string) {
	if img.Width <= 0 || img.Height <= 0 {
		return false, ""
	}
	if minSide > 0 && (img.Width < minSide || img.Height < minSide) {
		return true, "small_side"
	}
	if maxAspectRatio > 0 {
		longSide := img.Width
		shortSide := img.Height
		if shortSide > longSide {
			longSide, shortSide = shortSide, longSide
		}
		if float64(longSide)/float64(shortSide) > maxAspectRatio {
			return true, "extreme_aspect_ratio"
		}
	}
	return false, ""
}
