package service

import (
	"context"
	"fmt"
	"math"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
)

func (s *ImageMultimodalService) cachedImageVector(
	ctx context.Context,
	payload types.ImageMultimodalPayload,
	model embedding.Embedder,
	imageModel embedding.ImageEmbedder,
	prepared embedding.Image,
) ([][]float32, error) {
	identity, dimension := processingModelIdentity(model), model.GetDimensions()
	runtime := s.processingCache
	if identity == "" || dimension <= 0 {
		runtime = nil
	}
	validate := func(vector []*float32) error {
		if len(vector) == 0 || dimension > 0 && len(vector) != dimension {
			return fmt.Errorf("%d dimensions, index has %d", len(vector), dimension)
		}
		for _, v := range vector {
			if v == nil || math.IsNaN(float64(*v)) || math.IsInf(float64(*v), 0) {
				return fmt.Errorf("invalid image embedding value")
			}
		}
		return nil
	}
	vector, err := processingcache.Value(
		ctx,
		runtime,
		payload.TenantID,
		payload.KnowledgeID,
		"image.embedding.v1",
		struct {
			Model, Image, MIME string
			Dimension          int
		}{identity, processingcache.Digest(prepared.Data), prepared.MIMEType, dimension},
		validate,
		func(ctx context.Context) ([]*float32, error) {
			vectors, err := imageModel.BatchEmbedImages(ctx, []embedding.Image{prepared})
			if err != nil {
				return nil, err
			}
			if len(vectors) != 1 {
				return nil, fmt.Errorf("image model returned %d vectors for one image", len(vectors))
			}
			out := make([]*float32, len(vectors[0]))
			for i, v := range vectors[0] {
				value := v
				out[i] = &value
			}
			return out, nil
		},
	)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(vector))
	for i, v := range vector {
		out[i] = *v
	}
	return [][]float32{out}, nil
}
