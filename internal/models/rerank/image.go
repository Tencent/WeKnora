package rerank

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"golang.org/x/sync/errgroup"
)

// Image is one image document: its bytes and MIME type.
type Image = api.EmbedImage

// ErrImagesUnsupported is returned when a reranker is asked to score images
// its model or endpoint cannot take.
var ErrImagesUnsupported = errors.New("rerank model does not accept images")

// ImageReranker is the image side of a Reranker whose model scores an image
// against a text query by what it shows. Most rerankers have none; use
// AsImageReranker rather than a type assertion, since every decorator
// implements it whether or not the model underneath does.
type ImageReranker interface {
	// AcceptsImages reports whether both the model and its endpoint take
	// image documents.
	AcceptsImages() bool
	// ImageLimits are the vendor's documented per-image limits. The caller
	// prepares an image to fit; one outside them is refused.
	ImageLimits() imageprep.Limits
	// RerankImages scores images against the query on the same scale as
	// Rerank scores text, best first; indices refer to images.
	RerankImages(ctx context.Context, query string, images []Image) ([]RankResult, error)
}

// AsImageReranker returns r's image side when its model scores images.
func AsImageReranker(r Reranker) (ImageReranker, bool) {
	ir, ok := r.(ImageReranker)
	if !ok || !ir.AcceptsImages() {
		return nil, false
	}
	return ir, true
}

// imageSide is AsImageReranker for decorators, which must fail a call their
// inner reranker cannot serve.
func imageSide(r Reranker) (ImageReranker, error) {
	if ir, ok := AsImageReranker(r); ok {
		return ir, nil
	}
	return nil, fmt.Errorf("%s: %w", r.GetModelName(), ErrImagesUnsupported)
}

func imageLimitsOf(r Reranker) imageprep.Limits {
	if ir, ok := AsImageReranker(r); ok {
		return ir.ImageLimits()
	}
	return imageprep.Limits{}
}

func (r *protocolReranker) AcceptsImages() bool { return r.images != nil }

func (r *protocolReranker) ImageLimits() imageprep.Limits {
	return imageprep.LimitsOf(r.settings.ImageInput)
}

// RerankImages checks every image against the documented limits before
// sending any, splits them at the documented images per request, and puts
// the scores on the scale Rerank uses.
func (r *protocolReranker) RerankImages(ctx context.Context, query string, images []Image) ([]RankResult, error) {
	if r.images == nil {
		return nil, fmt.Errorf("%s: %w", r.modelName, ErrImagesUnsupported)
	}
	if len(images) == 0 {
		return nil, nil
	}
	for i, img := range images {
		if err := r.settings.CheckImage(img); err != nil {
			return nil, fmt.Errorf("%s rerank: image %d: %w", r.modelName, i, err)
		}
	}
	limit := r.settings.ImageBatchLimit()
	type batch struct{ start, end int }
	var batches []batch
	for start := 0; start < len(images); start += limit {
		batches = append(batches, batch{start, min(start+limit, len(images))})
	}
	scored := make([][]api.RerankResult, len(batches))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(r.concurrency())
	for i, b := range batches {
		group.Go(func() error {
			out, err := r.images.RerankImages(groupCtx, query, images[b.start:b.end])
			if err != nil {
				return err
			}
			for j := range out {
				out[j].Index += b.start
			}
			scored[i] = out
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	results := make([]RankResult, 0, len(images))
	for _, b := range scored {
		for _, item := range b {
			if item.Index < 0 || item.Index >= len(images) {
				return nil, fmt.Errorf("%s rerank: index %d out of range for %d images",
					r.modelName, item.Index, len(images))
			}
			results = append(results, RankResult{
				Index:          item.Index,
				RelevanceScore: normalizeScore(item.Score, r.settings.ScoreScale),
			})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	return results, nil
}

// describeImages stands in for images in logs and traces, which must not
// carry their bytes.
func describeImages(images []Image) []string {
	out := make([]string, len(images))
	for i, img := range images {
		out[i] = fmt.Sprintf("<%s, %d bytes>", img.MIMEType, len(img.Data))
	}
	return out
}

func (d *debugReranker) AcceptsImages() bool {
	_, ok := AsImageReranker(d.inner)
	return ok
}

func (d *debugReranker) ImageLimits() imageprep.Limits { return imageLimitsOf(d.inner) }

func (d *debugReranker) RerankImages(ctx context.Context, query string, images []Image) ([]RankResult, error) {
	inner, err := imageSide(d.inner)
	if err != nil {
		return nil, err
	}
	result, err := inner.RerankImages(ctx, query, images)
	logRerankDebug(ctx, d.inner.GetModelName(), query, describeImages(images), result, err, 0)
	return result, err
}

func (l *langfuseReranker) AcceptsImages() bool {
	_, ok := AsImageReranker(l.inner)
	return ok
}

func (l *langfuseReranker) ImageLimits() imageprep.Limits { return imageLimitsOf(l.inner) }

func (l *langfuseReranker) RerankImages(ctx context.Context, query string, images []Image) ([]RankResult, error) {
	inner, err := imageSide(l.inner)
	if err != nil {
		return nil, err
	}
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return inner.RerankImages(ctx, query, images)
	}
	described := describeImages(images)
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "rerank.images",
		Model: l.inner.GetModelName(),
		Input: map[string]interface{}{
			"query":          query,
			"document_count": len(images),
			"images":         previewDocs(described, langfuseRerankPreviewDocs),
		},
		Metadata: map[string]interface{}{"model_id": l.inner.GetModelID()},
	})
	results, err := inner.RerankImages(genCtx, query, images)
	gen.Finish(map[string]interface{}{
		"results":     summarizeResults(results, described, langfuseRerankMaxScores),
		"total_count": len(results),
		"score_stats": scoreStats(results),
	}, nil, err)
	return results, err
}
