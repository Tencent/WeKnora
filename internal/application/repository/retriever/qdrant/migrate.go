package qdrant

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/qdrant/go-client/qdrant"
)

// MigrateBM25 copies collections to a new prefix, retaining point IDs, payloads and
// dense vectors while adding BM25 vectors. Stop writers for the entire migration
// and cutover. Resume is only for a destination from an interrupted run of this migration.
// Source collections are never modified; progress is returned even on failure.
func MigrateBM25(ctx context.Context, client *qdrant.Client, source, target string, resume bool) (uint64, error) {
	if source == "" || target == "" || strings.HasPrefix(source, target) || strings.HasPrefix(target, source) {
		return 0, fmt.Errorf("source and target prefixes must be nonempty and nonoverlapping for legacy search")
	}
	for _, prefix := range []string{source, target} {
		if err := types.ValidateIndexConfig(types.IndexConfig{CollectionPrefix: prefix}); err != nil {
			return 0, err
		}
	}
	collections, err := client.ListCollections(ctx)
	if err != nil {
		return 0, err
	}
	slices.Sort(collections)
	for _, name := range collections {
		dimension, ok := collectionDimension(name, target)
		if !ok {
			continue
		}
		if !resume {
			return 0, fmt.Errorf("destination %s already exists; use resume only for your interrupted migration", name)
		}
		if !slices.Contains(collections, fmt.Sprintf("%s_%d", source, dimension)) {
			return 0, fmt.Errorf("destination %s has no corresponding source collection", name)
		}
	}
	var total uint64
	matched := false
	for _, name := range collections {
		dimension, ok := collectionDimension(name, source)
		if !ok {
			continue
		}
		matched = true
		copied, err := migrateBM25Collection(ctx, client, name, target, dimension, resume)
		total += copied
		if err != nil {
			return total, fmt.Errorf("migrate %s: %w", name, err)
		}
	}
	if !matched {
		return 0, fmt.Errorf("no dimension collections found for prefix %s", source)
	}
	return total, nil
}

func migrateBM25Collection(
	ctx context.Context, client *qdrant.Client, source, targetPrefix string, dimension int, resume bool,
) (uint64, error) {
	info, err := client.GetCollectionInfo(ctx, source)
	if err != nil {
		return 0, err
	}
	params := info.GetConfig().GetParams()
	dense := params.GetVectorsConfig().GetParams()
	if dense.GetSize() != uint64(dimension) || dense.GetDistance() != qdrant.Distance_Cosine {
		return 0, fmt.Errorf("source must have an unnamed cosine vector of dimension %d", dimension)
	}
	repo := &qdrantRepository{
		client: client, collectionBaseName: targetPrefix, bm25: true,
		shardNumber: int(params.GetShardNumber()), replicationFactor: int(params.GetReplicationFactor()),
	}
	target := repo.getCollectionName(dimension)
	exists, err := client.CollectionExists(ctx, target)
	if err != nil {
		return 0, err
	}
	if exists && !resume {
		return 0, fmt.Errorf("destination %s already exists; use resume only for your interrupted migration", target)
	}
	if err := repo.ensureCollection(ctx, dimension); err != nil {
		return 0, err
	}
	var copied uint64
	var offset *qdrant.PointId
	limit, wait := uint32(64), true
	seen := make(map[string]bool)
	for {
		points, next, err := client.ScrollAndOffset(ctx, &qdrant.ScrollPoints{
			CollectionName: source, Offset: offset, Limit: &limit,
			WithPayload: qdrant.NewWithPayload(true), WithVectors: qdrant.NewWithVectors(true),
		})
		if err != nil {
			return copied, err
		}
		batch := make([]*qdrant.PointStruct, 0, len(points))
		for _, point := range points {
			vector := denseVectorData(point.Vectors)
			if len(vector) != dimension {
				return copied, fmt.Errorf("point %s has no valid dense vector", point.Id)
			}
			batch = append(batch, &qdrant.PointStruct{
				Id: point.Id, Payload: point.Payload,
				Vectors: repo.pointVectors(vector, point.Payload[fieldContent].GetStringValue()),
			})
		}
		if len(batch) > 0 {
			if _, err := client.Upsert(ctx, &qdrant.UpsertPoints{
				CollectionName: target, Points: batch, Wait: &wait,
			}); err != nil {
				return copied, err
			}
			copied += uint64(len(batch))
		}
		if next == nil {
			break
		}
		if seen[next.String()] {
			return copied, fmt.Errorf("scroll did not advance at %s", next)
		}
		seen[next.String()] = true
		offset = next
	}
	for _, collection := range []string{source, target} {
		count, err := client.Count(ctx, &qdrant.CountPoints{CollectionName: collection, Exact: &wait})
		if err != nil {
			return copied, err
		}
		if count != copied {
			return copied, fmt.Errorf("%s has %d points, copied %d; keep writers stopped and inspect before cutover",
				collection, count, copied)
		}
	}
	return copied, nil
}
