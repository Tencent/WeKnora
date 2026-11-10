package handler

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// embeddingSimilarity is one scored candidate of an embedding similarity test.
type embeddingSimilarity struct {
	// Index is the candidate's position in the request: the documents in
	// order, then the image.
	Index      int     `json:"index"`
	Kind       string  `json:"kind"`
	Content    string  `json:"content,omitempty"`
	Similarity float64 `json:"similarity"`
}

// debugEmbeddingSimilarity embeds the input as a search query and every
// candidate as indexed content, the two sides retrieval uses, and returns
// the candidates by cosine similarity.
func (h *ModelHandler) debugEmbeddingSimilarity(
	c *gin.Context, id, query string, documents []string, fileBytes []byte,
	started time.Time, requestPreview, observations gin.H,
) {
	ctx := c.Request.Context()
	if strings.TrimSpace(query) == "" || (len(documents) == 0 && len(fileBytes) == 0) {
		_ = c.Error(errors.NewBadRequestError("query and at least one candidate are required"))
		return
	}
	instance, err := h.service.GetEmbeddingModel(ctx, id)
	if err != nil {
		writeModelDebugResult(c, started, requestPreview, nil, err, observations)
		return
	}
	var (
		imageModel embedding.ImageEmbedder
		prepared   embedding.Image
	)
	if len(fileBytes) > 0 {
		var ok bool
		if imageModel, ok = embedding.AsImageEmbedder(instance); !ok {
			_ = c.Error(errors.NewBadRequestError(embedding.ErrImagesUnsupported.Error()))
			return
		}
		if prepared, err = imageprep.Prepare(fileBytes, imageModel.ImageLimits()); err != nil {
			_ = c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
	}

	queryVector, err := instance.Embed(types.WithEmbedQuery(ctx), query)
	if err != nil {
		writeModelDebugResult(c, started, requestPreview, nil, err, observations)
		return
	}
	observations["dimension"] = len(queryVector)

	results := make([]embeddingSimilarity, 0, len(documents)+1)
	score := func(index int, kind, content string, vector []float32) error {
		// A width that differs from the query's means the two sides are not
		// in one space; a cosine of 0 would hide that.
		if len(vector) != len(queryVector) {
			return fmt.Errorf("%s candidate %d has %d dimensions, the query %d",
				kind, index, len(vector), len(queryVector))
		}
		results = append(results, embeddingSimilarity{
			Index: index, Kind: kind, Content: content,
			Similarity: types.CosineSimilarity(queryVector, vector),
		})
		return nil
	}
	if len(documents) > 0 {
		vectors, err := instance.BatchEmbed(ctx, documents)
		if err == nil && len(vectors) != len(documents) {
			err = fmt.Errorf("%d vectors for %d documents", len(vectors), len(documents))
		}
		for i := 0; err == nil && i < len(documents); i++ {
			err = score(i, "text", documents[i], vectors[i])
		}
		if err != nil {
			writeModelDebugResult(c, started, requestPreview, nil, err, observations)
			return
		}
	}
	if imageModel != nil {
		vectors, err := imageModel.BatchEmbedImages(ctx, []embedding.Image{prepared})
		if err == nil && len(vectors) != 1 {
			err = fmt.Errorf("%d vectors for 1 image", len(vectors))
		}
		if err == nil {
			err = score(len(documents), "image", "", vectors[0])
		}
		if err != nil {
			writeModelDebugResult(c, started, requestPreview, nil, err, observations)
			return
		}
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Similarity > results[j].Similarity })
	observations["result_count"] = len(results)
	writeModelDebugResult(c, started, requestPreview, gin.H{"results": results}, nil, observations)
}
