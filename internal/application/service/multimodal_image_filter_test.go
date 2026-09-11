package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
)

func storedImageWithSize(width, height int) docparser.StoredImage {
	return docparser.StoredImage{
		OriginalRef: fmt.Sprintf("img-%dx%d", width, height),
		ServingURL:  fmt.Sprintf("local://images/%dx%d.png", width, height),
		MimeType:    "image/png",
		Width:       width,
		Height:      height,
	}
}

func TestShouldSkipMultimodalImage(t *testing.T) {
	tests := []struct {
		name           string
		img            docparser.StoredImage
		minSide        int
		maxAspectRatio float64
		wantSkip       bool
		wantReason     string
	}{
		{
			name:           "normal image kept",
			img:            storedImageWithSize(800, 600),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       false,
		},
		{
			name:           "width below min side",
			img:            storedImageWithSize(16, 600),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       true,
			wantReason:     "small_side",
		},
		{
			name:           "height below min side",
			img:            storedImageWithSize(600, 16),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       true,
			wantReason:     "small_side",
		},
		{
			name:           "side exactly at min side boundary kept",
			img:            storedImageWithSize(32, 32),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       false,
		},
		{
			name:           "horizontal separator strip",
			img:            storedImageWithSize(2000, 40),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       true,
			wantReason:     "extreme_aspect_ratio",
		},
		{
			name:           "vertical separator strip",
			img:            storedImageWithSize(40, 2000),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       true,
			wantReason:     "extreme_aspect_ratio",
		},
		{
			name:           "aspect ratio exactly at boundary kept",
			img:            storedImageWithSize(640, 32),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       false,
		},
		{
			name:           "unknown dimensions kept",
			img:            storedImageWithSize(0, 0),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       false,
		},
		{
			name:           "partially unknown dimensions kept",
			img:            storedImageWithSize(0, 10),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       false,
		},
		{
			name:           "min side disabled",
			img:            storedImageWithSize(4, 4),
			minSide:        0,
			maxAspectRatio: 20,
			wantSkip:       false,
		},
		{
			name:           "aspect ratio disabled",
			img:            storedImageWithSize(10000, 40),
			minSide:        32,
			maxAspectRatio: 0,
			wantSkip:       false,
		},
		{
			name:           "small side check wins over aspect ratio",
			img:            storedImageWithSize(10, 1000),
			minSide:        32,
			maxAspectRatio: 20,
			wantSkip:       true,
			wantReason:     "small_side",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skip, reason := shouldSkipMultimodalImage(tt.img, tt.minSide, tt.maxAspectRatio)
			if skip != tt.wantSkip {
				t.Fatalf("shouldSkipMultimodalImage() skip = %v, want %v", skip, tt.wantSkip)
			}
			if skip && reason != tt.wantReason {
				t.Fatalf("shouldSkipMultimodalImage() reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

func TestFilterMultimodalImages(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input", func(t *testing.T) {
		if got := filterMultimodalImages(ctx, nil); got != nil {
			t.Fatalf("filterMultimodalImages(nil) = %v, want nil", got)
		}
	})

	t.Run("keeps images under default thresholds", func(t *testing.T) {
		images := []docparser.StoredImage{
			storedImageWithSize(800, 600),
			storedImageWithSize(1024, 768),
		}
		got := filterMultimodalImages(ctx, images)
		if len(got) != len(images) {
			t.Fatalf("kept %d images, want %d", len(got), len(images))
		}
	})

	t.Run("drops small and strip images", func(t *testing.T) {
		images := []docparser.StoredImage{
			storedImageWithSize(800, 600),  // keep
			storedImageWithSize(16, 16),    // small side
			storedImageWithSize(4000, 100), // aspect ratio 40 > 20
			storedImageWithSize(640, 480),  // keep
		}
		got := filterMultimodalImages(ctx, images)
		if len(got) != 2 {
			t.Fatalf("kept %d images, want 2", len(got))
		}
		if got[0] != images[0] || got[1] != images[3] {
			t.Fatalf("kept wrong images: %v", got)
		}
	})

	t.Run("enforces per-document cap", func(t *testing.T) {
		t.Setenv("WEKNORA_MULTIMODAL_MAX_IMAGES_PER_DOCUMENT", "3")
		images := make([]docparser.StoredImage, 0, 5)
		for i := 0; i < 5; i++ {
			images = append(images, storedImageWithSize(800+i, 600))
		}
		got := filterMultimodalImages(ctx, images)
		if len(got) != 3 {
			t.Fatalf("kept %d images, want cap of 3", len(got))
		}
		// Cap keeps the first images in document order.
		for i := range got {
			if got[i] != images[i] {
				t.Fatalf("kept image %d = %v, want %v", i, got[i], images[i])
			}
		}
	})

	t.Run("filtered images do not consume cap slots", func(t *testing.T) {
		t.Setenv("WEKNORA_MULTIMODAL_MAX_IMAGES_PER_DOCUMENT", "1")
		images := []docparser.StoredImage{
			storedImageWithSize(8, 8),     // filtered by size, must not take the slot
			storedImageWithSize(800, 600), // keep
			storedImageWithSize(640, 480), // over cap
		}
		got := filterMultimodalImages(ctx, images)
		if len(got) != 1 || got[0] != images[1] {
			t.Fatalf("kept %v, want only the first well-formed image", got)
		}
	})

	t.Run("cap of zero disables the limit", func(t *testing.T) {
		t.Setenv("WEKNORA_MULTIMODAL_MAX_IMAGES_PER_DOCUMENT", "0")
		images := make([]docparser.StoredImage, 0, 300)
		for i := 0; i < 300; i++ {
			images = append(images, storedImageWithSize(800, 600))
		}
		if got := filterMultimodalImages(ctx, images); len(got) != 300 {
			t.Fatalf("kept %d images, want all 300 with cap disabled", len(got))
		}
	})

	t.Run("env overrides for size thresholds", func(t *testing.T) {
		t.Setenv("WEKNORA_MULTIMODAL_MIN_IMAGE_SIDE", "100")
		t.Setenv("WEKNORA_MULTIMODAL_MAX_IMAGE_ASPECT_RATIO", "2")
		images := []docparser.StoredImage{
			storedImageWithSize(90, 90),   // below min side 100
			storedImageWithSize(500, 200), // aspect 2.5 > 2
			storedImageWithSize(400, 300), // keep
		}
		got := filterMultimodalImages(ctx, images)
		if len(got) != 1 || got[0] != images[2] {
			t.Fatalf("kept %v, want only the 400x300 image", got)
		}
	})

	t.Run("invalid env values fall back to defaults", func(t *testing.T) {
		t.Setenv("WEKNORA_MULTIMODAL_MAX_IMAGES_PER_DOCUMENT", "bad")
		t.Setenv("WEKNORA_MULTIMODAL_MIN_IMAGE_SIDE", "-5")
		t.Setenv("WEKNORA_MULTIMODAL_MAX_IMAGE_ASPECT_RATIO", "")
		if got := multimodalMaxImagesPerDocument(); got != defaultMultimodalMaxImagesPerDocument {
			t.Fatalf("max images = %d, want default %d", got, defaultMultimodalMaxImagesPerDocument)
		}
		if got := multimodalMinImageSide(); got != defaultMultimodalMinImageSide {
			t.Fatalf("min side = %d, want default %d", got, defaultMultimodalMinImageSide)
		}
		if got := multimodalMaxImageAspectRatio(); got != defaultMultimodalMaxImageAspectRatio {
			t.Fatalf("max aspect ratio = %v, want default %v", got, defaultMultimodalMaxImageAspectRatio)
		}
	})
}
