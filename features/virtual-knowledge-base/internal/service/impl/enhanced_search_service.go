package impl

import (
	"context"
	"errors"
	"math"

	docRepo "github.com/tencent/weknora/features/virtualkb/internal/repository/interfaces"
	vkbRepo "github.com/tencent/weknora/features/virtualkb/internal/repository/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// EnhancedSearchService provides tag-weighted search capabilities.
type EnhancedSearchService struct {
	docRepo docRepo.DocumentTagRepository
	vkbRepo vkbRepo.VirtualKBRepository
}

// NewEnhancedSearchService constructs a search service.
func NewEnhancedSearchService(docRepository docRepo.DocumentTagRepository, vkbRepository vkbRepo.VirtualKBRepository) *EnhancedSearchService {
	return &EnhancedSearchService{docRepo: docRepository, vkbRepo: vkbRepository}
}

// Search performs an enhanced search with tag weighting.
func (s *EnhancedSearchService) Search(ctx context.Context, request *types.EnhancedSearchRequest) (*types.EnhancedSearchResponse, error) {
	if request == nil {
		return nil, errors.New("search request is required")
	}
	if request.VirtualKBID == nil && len(request.TagFilters) == 0 {
		return nil, errors.New("either virtual_kb_id or tag_filters must be provided")
	}

	filters := request.TagFilters
	if request.VirtualKBID != nil {
		vkb, err := s.vkbRepo.GetByID(ctx, *request.VirtualKBID)
		if err != nil {
			return nil, err
		}
		filters = vkb.Filters
	}

	scores := make(map[string]float64)
	for _, filter := range filters {
		for _, tagID := range filter.TagIDs {
			documents, err := s.docRepo.ListDocumentsByTag(ctx, tagID)
			if err != nil {
				return nil, err
			}

			for _, doc := range documents {
				weight := filter.Weight
				if doc.Weight != nil {
					weight *= *doc.Weight
				}
				scores[doc.DocumentID] += weight
			}
		}
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 20
	}

	top := topScores(scores, limit)
	return &types.EnhancedSearchResponse{Results: top}, nil
}

func topScores(scores map[string]float64, limit int) []types.DocumentScore {
	list := make([]types.DocumentScore, 0, len(scores))
	for docID, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			continue
		}
		list = append(list, types.DocumentScore{DocumentID: docID, Score: score})
	}

	if len(list) <= limit {
		return list
	}

	partialSelect(list, limit)
	return list[:limit]
}

func partialSelect(items []types.DocumentScore, limit int) {
	if limit >= len(items) {
		return
	}
	heapify(items)
	for i := len(items) - 1; i >= len(items)-limit; i-- {
		items[0], items[i] = items[i], items[0]
		siftDown(items, 0, i)
	}
}

func heapify(items []types.DocumentScore) {
	for i := len(items)/2 - 1; i >= 0; i-- {
		siftDown(items, i, len(items))
	}
}

func siftDown(items []types.DocumentScore, root, length int) {
	for {
		left := 2*root + 1
		right := left + 1
		largest := root

		if left < length && items[left].Score > items[largest].Score {
			largest = left
		}
		if right < length && items[right].Score > items[largest].Score {
			largest = right
		}
		if largest == root {
			return
		}
		items[root], items[largest] = items[largest], items[root]
		root = largest
	}
}
